# Cloud-Z

Cloud-Z gathers information and perform benchmarks on cloud instances in multiple cloud providers.

- [x] Cloud type, instance id, and type
- [ ] CPU information including type, number of available cores, and cache sizes
- [ ] Benchmark CPU
- [ ] Storage devices information
- [ ] Benchmark storage
- [ ] Network devices information
- [ ] Benchmark network

### Supported clouds:

* Amazon Web Services (AWS)
* Google Cloud Platform (GCP)
* Microsoft Azure

### Supported platforms:

* Windows
  * x86_64
  * arm64
* Linux
  * x86_64
  * arm64

[![CI](https://github.com/CloudSnorkel/cloud-z/actions/workflows/goreleaser.yml/badge.svg)](https://github.com/CloudSnorkel/cloud-z/actions/workflows/goreleaser.yml)

## Usage

Cloud-Z is provided as a single binary that can be downloaded from the [releases page](https://github.com/CloudSnorkel/cloud-z/releases).

```bash
$ ./cloud-z
+---------------+-----------------------+
| Cloud         | AWS                   |
| AMI           | ami-0712c70d31ba14f8a |
| Instance ID   | i-12345678900112344   |
| Instance type | t4g.nano              |
+---------------+-----------------------+
```
