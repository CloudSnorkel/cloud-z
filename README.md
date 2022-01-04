# Cloud-Z

Cloud-Z gathers information and perform benchmarks on cloud instances in multiple cloud providers.

- [x] Cloud type, instance id, and type
- [x] CPU information including type, number of available cores, and cache sizes
- [x] RAM information
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
+-----------------+--------------------------------+
| CPU             | Intel(R) Xeon(R) CPU @ 2.20GHz |
| Vendor          | GenuineIntel                   |
| Vendor ID       | Intel                          |
| Family          |                              6 |
| MHz             |                           2200 |
| Logical cores   |                              2 |
| Physical cores  |                              1 |
| Thread per core |                              2 |
| Boost frequency |                              0 |
| L1 Cache        | 32.00KB instruction, 32.00KB   |
|                 | data                           |
| L2 Cache        | 256.00KB                       |
| L2 Cache        | 256.00KB                       |
| L3 Cache        | 55.00MB                        |
| Cache line      |                             64 |
| Features        | ADX, AESNI, AVX, AVX2, BMI1,   |
|                 | BMI2, CLMUL, CMOV, CX16,       |
|                 | ERMS, F16C, FMA3, HLE, HTT,    |
|                 | HYPERVISOR, IBPB, LZCNT, MMX,  |
|                 | MMXEXT, NX, POPCNT, RDRAND,    |
|                 | RDSEED, RDTSCP, RTM, SSE,      |
|                 | SSE2, SSE3, SSE4, SSE42,       |
|                 | SSSE3, STIBP                   |
+-----------------+--------------------------------+
+-----------------------+----------+
| Total RAM             | 1.0GB    |
| Stick #1: location    | DIMM 0   |
| Stick #1: type        | RAM DIMM |
| Stick #1: size        | 1024MB   |
| Stick #1: data width  | 64-bit   |
| Stick #1: total width | 64-bit   |
| Stick #1: speed       | 0 MHz    |
+-----------------------+----------+
```
