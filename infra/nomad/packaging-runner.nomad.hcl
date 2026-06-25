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
