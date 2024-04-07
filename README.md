# nodemanager

An approach to managing node configurations using Kubernetes resources.

## Description

This controller can run either on cluster nodes, or on off-cluster nodes and
features a small package/file/service resource footprint to allow a collections
of `ConfigSet` resources to manage portions of the node configuration, based on
matching labels in the `ConfigSet` resources to the `ManagedNode` resources for
the instance of the controller. This allows a very flexible configuration
management approach using Kubernetes to store configuration data to be
referenced from `Secret` or `ConfigMap` and templated into files on disk of the
nodes. Services can "subscribe" to changes on files, so that they are restarted
when details changed.

In a small lab environment, this has been very productive in capturing all of
the various configuration details of the Kubernetes nodes, as well as
supporting nodes that are not running in the cluster, such as FreeBSD storage
nodes. This includes configuration details like authentication, package
installation, ssh configuration, NTP configuration, etc.

Currently targeted to for support in this project are Archlinux and FreeBSD,
but the interfaces exist in such a way to allow expanded operating system
support quite easily. Contributions welcome.

Additionally, currently only `amd64` architectures are built, with the
expectation of `arm` packages at some point in the future.

This project is built using `kubebuilder`, continue reading to get started.

### Example

Consider the following `ConfigSet` to manage the clock on some Linux machines.

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  labels:
    kubernetes.io/os: arch
  name: clock-linux
  namespace: nodemanager
spec:
  packages:
    - ensure: installed
      name: chrony
  services:
    - enable: true
      ensure: running
      name: chronyd
    - enable: false
      ensure: stopped
      name: systemd-timesyncd
```

On all nodes matching the label selector, the `chrony` package is installed and
we manage the service, ensuring to stop the conflicting `systemd-timesyncd`
service.

## Getting Started

You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster

1. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
```

2. Build and push your image to the location specified by `IMG`:

```sh
make docker-build docker-push IMG=<some-registry>/nodemanager:tag
```

3. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/nodemanager:tag
```

### Uninstall CRDs

To delete the CRDs from the cluster:

```sh
make uninstall
```

### Undeploy controller

UnDeploy the controller to the cluster:

```sh
make undeploy
```

## Contributing

I'd welcome PRs or issues, if folks end up testing this out.

### How it works

This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
which provides a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

For nodes running off-cluster, this implies that the configuration is checked periodically to confirm compliance against the Kubernetes resources. The same is true for controllers running on cluster.

### Test It Out

1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions

If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
