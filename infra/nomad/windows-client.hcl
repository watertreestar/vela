datacenter = "dc1"
data_dir   = "C:/nomad/data"
bind_addr  = "0.0.0.0"

client {
  enabled = true
  servers = ["127.0.0.1:4647"]

  meta {
    node_class = "windows-packager"
  }
}

plugin "raw_exec" {
  config {
    enabled = true
  }
}
