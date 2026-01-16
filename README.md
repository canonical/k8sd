# k8sd

k8sd is a cluster-manager for Kubernetes and is primarily used in the [Canonical Kubernetes](https://github.com/canonical/k8s-snap) platform.

## Overview

k8sd provides cluster management capabilities for Kubernetes, handling bootstrapping, node joining, configuration management, and cluster lifecycle operations.

The API is defined and versioned in [github.com/canonical/k8s-snap-api](https://github.com/canonical/k8s-snap-api).

## Binaries

This project builds three main binaries:

- **k8sd**: The main daemon that manages the Kubernetes cluster
- **k8s**: The CLI tool for interacting with k8sd and managing the cluster
- **k8s-apiserver-proxy**: A proxy component for the Kubernetes API server

## Building

### Dynamic builds

Build with dynamic linking

```bash
make dynamic
```

The compiled binaries will be placed in the `bin/dynamic/` directory.

### Static builds

Build with static linking

```bash
make static
```

The compiled binaries will be placed in the `bin/static/` directory.

## Development

### Formatting

Format the code and tidy dependencies:

```bash
make go.fmt
```

### Linting

Run golangci-lint:

```bash
make go.lint
```

### Vetting

Run go vet:

```bash
make go.vet
```

### Testing

Run unit tests with coverage:

```bash
make go.unit
```

### Documentation generation

Generate documentation from code:

```bash
make go.doc
```

### Cleanup

Remove build artifacts:

```bash
make clean
```

## Community and support

Do you have questions about Canonical Kubernetes? Perhaps you'd like some advice from more experienced users or discuss how to achieve a certain goal? Get in touch on the [#canonical-kubernetes](https://kubernetes.slack.com/archives/CG1V2CAMB) channel on the [Kubernetes Slack workspace](http://slack.kubernetes.io/).

Please report any bugs and issues on the [k8s-snap repository](https://github.com/canonical/k8s-snap/issues).

Canonical Kubernetes is covered by the [Ubuntu Code of Conduct](https://ubuntu.com/community/ethos/code-of-conduct).

## Contribute

Canonical Kubernetes is a proudly open source project, and we welcome and encourage contributions to the code and documentation. If you are interested, take a look at our [contributing guide](https://documentation.ubuntu.com/canonical-kubernetes/latest/snap/howto/contribute/).

## License and copyright

Canonical Kubernetes is released under the [GPL-3.0 license](LICENSE).

© 2015-2026 Canonical Ltd.
