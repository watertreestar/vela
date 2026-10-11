#!/usr/bin/env python3
"""Run an isolated Nomad ACL and mutual-TLS acceptance on a Docker context."""

from __future__ import annotations

import argparse
import pathlib
import subprocess
import sys
import time
import uuid


NOMAD_VERSION = "1.10.5"
NOMAD_ARCHIVE = f"https://releases.hashicorp.com/nomad/{NOMAD_VERSION}/nomad_{NOMAD_VERSION}_linux_amd64.zip"
UTILITY_IMAGE = "golang:1.26.5-bookworm"
FILE_IMAGE = "redis:7.4-alpine"
CURL_IMAGE = "curlimages/curl:8.16.0"


def repo_root() -> pathlib.Path:
    return pathlib.Path(__file__).resolve().parents[1]


class DockerAcceptance:
    def __init__(self, context: str) -> None:
        suffix = uuid.uuid4().hex[:12]
        self.context = context
        self.prefix = f"vela-nomad-secure-{suffix}"
        self.network = f"{self.prefix}-net"
        self.volume = f"{self.prefix}-data"
        self.server = f"{self.prefix}-server"
        self.client = f"{self.prefix}-client"
        self.checker = f"{self.prefix}-check"
        self.label = f"vela.acceptance={self.prefix}"

    def docker(
        self,
        arguments: list[str],
        *,
        input_text: str | None = None,
        check: bool = True,
        capture: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            ["docker", "--context", self.context, *arguments],
            input=input_text,
            text=True,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
        )
        if check and result.returncode != 0:
            detail = "\n".join(part.strip() for part in (result.stdout, result.stderr) if part and part.strip())
            raise RuntimeError(detail or f"docker command exited {result.returncode}")
        return result

    def write_file(self, relative_path: str, content: str) -> None:
        self.docker(
            [
                "run",
                "--rm",
                "-i",
                "--user",
                "0:0",
                "-v",
                f"{self.volume}:/work",
                "--entrypoint",
                "sh",
                FILE_IMAGE,
                "-c",
                f"umask 077; tee /work/{relative_path} >/dev/null",
            ],
            input_text=content,
        )

    def tls_environment(self) -> list[str]:
        return [
            "-e",
            "NOMAD_ADDR=https://nomad-server:4646",
            "-e",
            "NOMAD_CACERT=/work/nomad-agent-ca.pem",
            "-e",
            "NOMAD_CLIENT_CERT=/work/global-client-nomad.pem",
            "-e",
            "NOMAD_CLIENT_KEY=/work/global-client-nomad-key.pem",
        ]

    def utility(self, command: str, *, capture: bool = True) -> subprocess.CompletedProcess[str]:
        return self.docker(
            [
                "run",
                "--rm",
                "--network",
                self.network,
                "-v",
                f"{self.volume}:/work",
                *self.tls_environment(),
                "--entrypoint",
                "sh",
                UTILITY_IMAGE,
                "-lc",
                command,
            ],
            capture=capture,
        )

    def create_resources(self) -> None:
        self.docker(["volume", "create", "--label", self.label, self.volume])
        self.docker(["network", "create", "--label", self.label, self.network])

    def install_nomad(self) -> None:
        print(f"[01/08] Download Nomad {NOMAD_VERSION} into the isolated volume")
        self.docker(
            [
                "run",
                "--rm",
                "--user",
                "0:0",
                "-v",
                f"{self.volume}:/work",
                CURL_IMAGE,
                "-fsSLo",
                "/work/nomad.zip",
                NOMAD_ARCHIVE,
            ]
        )
        self.docker(
            [
                "run",
                "--rm",
                "--user",
                "0:0",
                "-v",
                f"{self.volume}:/work",
                "--entrypoint",
                "sh",
                FILE_IMAGE,
                "-lc",
                "cd /work && unzip -q nomad.zip && chmod 0755 nomad && rm nomad.zip",
            ]
        )

    def generate_tls(self) -> None:
        print("[02/08] Generate an isolated CA, server identity, and client identity")
        base = ["run", "--rm", "-v", f"{self.volume}:/work", "-w", "/work", "--entrypoint", "/work/nomad", UTILITY_IMAGE]
        self.docker([*base, "tls", "ca", "create"])
        self.docker(
            [
                *base,
                "tls",
                "cert",
                "create",
                "-server",
                "-region",
                "global",
                "-additional-dnsname",
                "nomad-server",
                "-additional-dnsname",
                "localhost",
                "-additional-ipaddress",
                "127.0.0.1",
            ]
        )
        self.docker([*base, "tls", "cert", "create", "-client", "-region", "global"])

    def write_configuration(self) -> None:
        server_config = """
name = "vela-secure-server"
region = "global"
datacenter = "dc1"
data_dir = "/work/server-data"
bind_addr = "0.0.0.0"
disable_update_check = true

server {
  enabled = true
  bootstrap_expect = 1
}

acl {
  enabled = true
}

tls {
  http = true
  rpc = true
  ca_file = "/work/nomad-agent-ca.pem"
  cert_file = "/work/global-server-nomad.pem"
  key_file = "/work/global-server-nomad-key.pem"
  verify_https_client = true
  verify_server_hostname = true
}
""".strip()
        client_config = """
name = "vela-secure-linux-client"
region = "global"
datacenter = "dc1"
data_dir = "/work/client-data"
bind_addr = "0.0.0.0"
disable_update_check = true

client {
  enabled = true
  servers = ["nomad-server:4647"]
  meta {
    node_class = "linux-packager"
  }
}

tls {
  http = true
  rpc = true
  ca_file = "/work/nomad-agent-ca.pem"
  cert_file = "/work/global-client-nomad.pem"
  key_file = "/work/global-client-nomad-key.pem"
  verify_https_client = true
  verify_server_hostname = true
}
""".strip()
        read_policy = """
node {
  policy = "read"
}

agent {
  policy = "read"
}
""".strip()
        self.write_file("server.hcl", server_config + "\n")
        self.write_file("client.hcl", client_config + "\n")
        self.write_file("vela-read.hcl", read_policy + "\n")

    def start_server(self) -> None:
        print("[03/08] Start the isolated ACL-enabled mTLS Nomad server")
        self.docker(
            [
                "run",
                "-d",
                "--name",
                self.server,
                "--hostname",
                "nomad-server",
                "--label",
                self.label,
                "--network",
                self.network,
                "--network-alias",
                "nomad-server",
                "--cap-add",
                "IPC_LOCK",
                "-v",
                f"{self.volume}:/work",
                "--entrypoint",
                "/work/nomad",
                UTILITY_IMAGE,
                "agent",
                "-config=/work/server.hcl",
            ]
        )
        self.wait_until(
            lambda: self.utility(
                "curl -fsS --cacert /work/nomad-agent-ca.pem "
                "--cert /work/global-client-nomad.pem --key /work/global-client-nomad-key.pem "
                "https://nomad-server:4646/v1/status/leader | grep -q ':4647'",
                capture=True,
            ).returncode
            == 0,
            "Nomad leader",
        )

    def bootstrap_acl(self) -> None:
        print("[04/08] Bootstrap ACLs and mint a read-only client token")
        self.utility("/work/nomad acl bootstrap -json > /work/bootstrap.json")
        self.utility(
            "python3 -c 'import json,os,pathlib; p=pathlib.Path(\"/work/master.token\"); "
            "p.write_text(json.load(open(\"/work/bootstrap.json\"))[\"SecretID\"]); os.chmod(p,0o600)'"
        )
        self.utility(
            "export NOMAD_TOKEN=\"$(cat /work/master.token)\"; "
            "/work/nomad acl policy apply -description='Vela secure acceptance read policy' vela-read /work/vela-read.hcl >/dev/null"
        )
        self.utility(
            "export NOMAD_TOKEN=\"$(cat /work/master.token)\"; "
            "/work/nomad acl token create -name='Vela secure acceptance' -type=client -policy=vela-read -json > /work/read-token.json"
        )
        self.utility(
            "python3 -c 'import json,os,pathlib; p=pathlib.Path(\"/work/read.token\"); "
            "p.write_text(json.load(open(\"/work/read-token.json\"))[\"SecretID\"]); os.chmod(p,0o600)'"
        )
        self.utility("rm -f /work/bootstrap.json /work/read-token.json")

    def start_client(self) -> None:
        print("[05/08] Start a real Linux client through encrypted RPC")
        self.docker(
            [
                "run",
                "-d",
                "--name",
                self.client,
                "--hostname",
                "nomad-client",
                "--label",
                self.label,
                "--network",
                self.network,
                "--privileged",
                "--cgroupns",
                "host",
                "-v",
                f"{self.volume}:/work",
                "-v",
                "/sys/fs/cgroup:/sys/fs/cgroup:rw",
                "--entrypoint",
                "sh",
                UTILITY_IMAGE,
                "-lc",
                "apt-get update -qq && apt-get install -y -qq iproute2 >/dev/null && "
                "exec /work/nomad agent -config=/work/client.hcl",
            ]
        )
        self.wait_until(
            lambda: self.utility(
                "export NOMAD_TOKEN=\"$(cat /work/read.token)\"; /work/nomad node status 2>/dev/null | grep -q 'ready'",
                capture=True,
            ).returncode
            == 0,
            "ready Linux node",
            timeout=120,
        )

    def run_gate(self, script_path: pathlib.Path) -> None:
        print("[06/08] Copy the current readiness gate into the isolated network")
        command = (
            "export NOMAD_TOKEN=\"$(cat /work/read.token)\"; "
            "exec python3 /tmp/nomad_check_cli.py check --production "
            "--address https://nomad-server:4646 "
            "--ca-cert /work/nomad-agent-ca.pem "
            "--client-cert /work/global-client-nomad.pem "
            "--client-key /work/global-client-nomad-key.pem "
            "--require-token --require-acl --require-mtls --require-linux-node"
        )
        self.docker(
            [
                "create",
                "--name",
                self.checker,
                "--label",
                self.label,
                "--network",
                self.network,
                "-v",
                f"{self.volume}:/work",
                "--entrypoint",
                "sh",
                UTILITY_IMAGE,
                "-lc",
                command,
            ]
        )
        self.docker(["cp", str(script_path), f"{self.checker}:/tmp/nomad_check_cli.py"])
        print("[07/08] Prove ACL denial and mandatory client-certificate enforcement")
        result = self.docker(["start", "-a", self.checker], check=False)
        if result.stdout:
            print(result.stdout.rstrip())
        if result.stderr:
            print(result.stderr.rstrip(), file=sys.stderr)
        if result.returncode != 0:
            raise RuntimeError(f"Nomad secure readiness gate exited {result.returncode}")

    def wait_until(self, predicate, description: str, timeout: int = 60) -> None:
        deadline = time.monotonic() + timeout
        last_error = ""
        while time.monotonic() < deadline:
            try:
                if predicate():
                    return
            except RuntimeError as exc:
                last_error = str(exc)
            time.sleep(1)
        raise RuntimeError(f"timed out waiting for {description}: {last_error}".rstrip())

    def diagnostic_logs(self) -> None:
        for name in (self.server, self.client):
            exists = self.docker(["inspect", name], check=False).returncode == 0
            if not exists:
                continue
            result = self.docker(["logs", "--tail", "80", name], check=False)
            output = "\n".join(part for part in (result.stdout, result.stderr) if part).strip()
            if output:
                print(f"--- {name} logs ---\n{output}", file=sys.stderr)

    def cleanup(self) -> None:
        print("[08/08] Remove isolated containers, network, credentials, and certificates")
        for name in (self.checker, self.client, self.server):
            self.docker(["rm", "-f", name], check=False)
        self.docker(["network", "rm", self.network], check=False)
        self.docker(["volume", "rm", "-f", self.volume], check=False)

    def assert_clean(self) -> None:
        containers = self.docker(["ps", "-aq", "--filter", f"label={self.label}"], check=False).stdout.strip()
        networks = self.docker(["network", "ls", "-q", "--filter", f"label={self.label}"], check=False).stdout.strip()
        volumes = self.docker(["volume", "ls", "-q", "--filter", f"label={self.label}"], check=False).stdout.strip()
        if containers or networks or volumes:
            raise RuntimeError("isolated Nomad acceptance resources were not fully removed")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run isolated Nomad ACL and mTLS live acceptance.")
    parser.add_argument("--docker-context", default="dev-ubuntu")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    acceptance = DockerAcceptance(args.docker_context)
    passed = False
    try:
        acceptance.create_resources()
        acceptance.install_nomad()
        acceptance.generate_tls()
        acceptance.write_configuration()
        acceptance.start_server()
        acceptance.bootstrap_acl()
        acceptance.start_client()
        acceptance.run_gate(repo_root() / "scripts" / "nomad_check_cli.py")
        passed = True
    except Exception as exc:
        print(f"[FAIL] {exc}", file=sys.stderr)
        acceptance.diagnostic_logs()
    finally:
        acceptance.cleanup()
        try:
            acceptance.assert_clean()
        except Exception as exc:
            print(f"[FAIL] {exc}", file=sys.stderr)
            passed = False
    if passed:
        print("Nomad ACL and mTLS live acceptance passed with zero isolated-resource residue.")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
