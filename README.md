# static-webpage-k8s-operator

Purpose of this operator is deploy webserver with a static webpage.

**Note:** Current state of Operator is in alpha state:
- not very tested
- can have problems
- but can be used for not critical job


## Description

Operator deploys into cluster deployment, which is running pod with two containers:
nginx and git-sync.

Nginx container serve webpage, git-sync responsible for cloning repository, checkout then specific branch and then check for updates.

Nginx version nginx and git-sync is **hardcoded** - nginx:1.23.3 and registry.k8s.io/git-sync/git-sync:v3.6.4

Nginx running as is, more info you may find [here](https://hub.docker.com/_/nginx) . **Note** nginx will use as *index* `index.html` or `index.htm`

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

Use kubernetes 1.19+

### Running on the cluster
1. Deploy the controller to the cluster:

```sh
make deploy
```

2. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
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

If you want to some feature or know how to improve it create a PR.

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/) 
which provides a reconcile function responsible for synchronizing resources untile the desired state is reached on the cluster 

## RoadMap

- [ ] Add tests to code
- [ ] Add ability to use own nginx image
- [ ] Add ability to pull images from private repositories


## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

