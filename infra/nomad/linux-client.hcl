datacenter = "dc1"
data_dir   = "/opt/nomad/data"
bind_addr  = "0.0.0.0"

client {
  enabled = true
  servers = ["127.0.0.1:4647"]

  meta {
    node_class = "linux-packager"
  }
}

plugin "docker" {
  config {
    allow_privileged = true
  }
}
