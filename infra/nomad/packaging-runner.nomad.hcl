job "packaging-${PRODUCT_ID}-${BUILD_ID}" {
  type        = "batch"
  datacenters = ["dc1"]

  group "builder" {
    count = 1

    restart {
      attempts = 0
      mode     = "fail"
    }

    reschedule {
      attempts  = 0
      unlimited = false
    }

    ephemeral_disk {
      sticky  = false
      migrate = false
      size    = 4096
    }

    max_run_duration = "1h"

    task "package" {
      driver = "docker"

      config {
        image   = "${PACKAGING_RUNNER_IMAGE}"
        command = "sh"
        args = [
          "-lc",
          "${PACKAGING_RUNNER_COMMAND}"
        ]
      }

      identity {
        name = "runner-variable"
        file = true
        env  = false
      }

      template {
        data            = "{{ with nomadVar \"nomad/jobs/packaging-${PRODUCT_ID}-${BUILD_ID}/builder/package/runner\" }}{{ .runner_bearer }}{{ end }}"
        destination     = "secrets/vela-runner-auth"
        env             = false
        perms           = "0400"
        change_mode     = "noop"
        err_missing_key = true
      }

      template {
        data            = "{{ with nomadVar \"nomad/jobs/packaging-${PRODUCT_ID}-${BUILD_ID}/builder/package/runner\" }}{{ .execution_capability }}{{ end }}"
        destination     = "secrets/vela-runner-execution"
        env             = false
        perms           = "0400"
        change_mode     = "noop"
        err_missing_key = true
      }

      env {
        BUILD_ID     = "${BUILD_ID}"
        PACKAGING_API_URL = "${PACKAGING_API_URL}"
        RUNNER_ID    = "${RUNNER_ID}"
        PRODUCT_ID   = "${PRODUCT_ID}"
        TARGET       = "${TARGET}"
        PROFILE      = "${PROFILE}"
        CUSTOMER     = "${CUSTOMER}"
        VERSION      = "${VERSION}"
        MINIO_BUCKET = "${MINIO_BUCKET}"
        PACKAGING_API_TOKEN_FILE = "vela-runner-auth"
        RUNNER_EXECUTION_FILE = "vela-runner-execution"
        RUNNER_AUTH_REQUIRED = "true"
      }

      resources {
        cpu    = 2000
        memory = 4096
        disk   = 4096
      }

      logs {
        max_files     = 10
        max_file_size = 20
      }

      kill_timeout = "30s"
    }
  }
}
