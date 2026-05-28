job "packaging-${PRODUCT_ID}-${BUILD_ID}" {
  type        = "batch"
  datacenters = ["dc1"]

  group "builder" {
    count = 1

    restart {
      attempts = 1
      mode     = "fail"
    }

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
      }
    }
  }
}
