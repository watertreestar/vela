# MinIO Layout

Suggested buckets:

- `service-artifacts` - inputs produced by service repositories.
- `packaging-artifacts` - final installers and build metadata.

Input layout:

```text
service-artifacts/
  Publish/
    engine/main/latest/location-engine-1.2.3.tar.gz
    location-guard/main/latest/location-guard-1.2.3.tar.gz
```

Output layout:

```text
packaging-artifacts/
  products/
    lsap/
      qa/
        1.2.3/
          linux/
            lsap-linux-x86_64-1.2.3.tar.gz
            lsap-linux-x86_64-1.2.3.sha256
            build-info.json
          windows/
            lsap-windows-x64-1.2.3-setup.exe
            lsap-windows-x64-1.2.3-setup.sha256
            build-info.json
```

Build metadata should include:

- build id
- product id
- target
- profile
- customer
- source artifact URIs
- generated installer URI
- checksum
- git commit of the packaging system
- runner id
