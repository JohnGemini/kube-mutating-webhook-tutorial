# Kubernetes Validating Admission Webhook for flavor validation

## Prerequisites

Kubernetes 1.9.0 or above with the `admissionregistration.k8s.io/v1beta1` API enabled. Verify that by the following command:

```bash
kubectl api-versions | grep admissionregistration.k8s.io/v1beta1
```

The result should be:

```bash
admissionregistration.k8s.io/v1beta1
```

In addition, the `MutatingAdmissionWebhook` and `ValidatingAdmissionWebhook` admission controllers should be added and listed in the correct order in the admission-control flag of kube-apiserver.

## Build

1. Setup dep

    The repo uses [dep](https://github.com/golang/dep) as the dependency management tool for its Go codebase. Install `dep` by the following command:

    ```bash
    go get -u github.com/golang/dep/cmd/dep
    ```

2. Build and save the docker image

    ```bash
    make
    ```

    or specify the image:tag

    ```bash
    make image=hook tag=1.0
    ```

## Deploy

Create a signed cert/key pair and store it in a Kubernetes `secret` that will be consumed by webhook deployment. And then, deploy resources

``` bash
./deployment/install.sh
```
