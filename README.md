# GKE VPC Firewall Rules Controller

A Kubernetes controller that automatically creates VPC firewall rules when GKE Gateway resources are deployed in a shared VPC environment.

## Problem

When using GKE Gateway (internal or regional external Application Load Balancer) in a **shared VPC**, GKE does not automatically create the ingress firewall rules needed for proxy traffic to reach pods. Without these rules, traffic from the load balancer's proxy-only subnet is blocked from reaching the pod network.

## What It Does

The controller watches for Kubernetes `Gateway` resources with the following GatewayClasses:

- `gke-l7-rilb` (internal Application Load Balancer)
- `gke-l7-regional-external-managed` (regional external Application Load Balancer)

When such a Gateway exists, it automatically creates a VPC firewall rule in the **shared VPC host project** with:

| Field | Value |
|-------|-------|
| Direction | `INGRESS` |
| Source ranges | Proxy-only subnet CIDR (`purpose=REGIONAL_MANAGED_PROXY`) |
| Destination ranges | Cluster pod CIDR |
| Protocol | TCP (all ports) |
| Priority | 1000 |

The firewall rule is named `gke-<cluster-name>-gw-proxy-to-pods` and is fully idempotent — creating, updating, or removing it based on the presence of matching Gateway resources.

When all matching Gateways are deleted, the firewall rule is cleaned up automatically.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│ GKE Cluster (Service Project)                           │
│                                                         │
│  ┌──────────────────────┐    ┌────────────────────┐     │
│  │ Gateway Resources    │    │ Controller Pod      │     │
│  │ (gke-l7-rilb, etc.)  ├───►│                    │     │
│  └──────────────────────┘    │ 1. Watch Gateways  │     │
│                              │ 2. Discover subnet │     │
│                              │ 3. Manage firewall │     │
│                              └────────┬───────────┘     │
└───────────────────────────────────────┼─────────────────┘
                                        │ GCP APIs
                                        ▼
┌─────────────────────────────────────────────────────────┐
│ Shared VPC Host Project                                 │
│                                                         │
│  ┌─────────────────────┐    ┌────────────────────────┐  │
│  │ Proxy-Only Subnet   │    │ Firewall Rule          │  │
│  │ (REGIONAL_MANAGED_  │    │ gke-<cluster>-gw-      │  │
│  │  PROXY)             │    │ proxy-to-pods          │  │
│  └─────────────────────┘    └────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### Reconciliation Flow

1. On any Gateway create/update/delete event (or periodic resync), the controller lists all Gateways across all namespaces.
2. Filters to those matching the supported GatewayClasses.
3. If any matching Gateways exist: discovers the proxy-only subnet CIDR and ensures the firewall rule is present and correct.
4. If no matching Gateways exist: deletes the firewall rule.
5. Periodic resync (default: 20 minutes) ensures the firewall rule is self-healing even if modified or deleted out-of-band.

### Discovery

At startup, the controller reads its identity from the **GCP metadata server** (project, zone, cluster name), then calls the **GKE Container API** to resolve the VPC network, pod CIDR, and shared VPC host project. The proxy-only subnet is discovered via the **Compute Subnets API** and cached for 5 minutes.

## Prerequisites

- GKE cluster in a **shared VPC** with Workload Identity enabled
- Gateway API CRDs installed on the cluster (`gateway.networking.k8s.io/v1`)
- A proxy-only subnet (`purpose=REGIONAL_MANAGED_PROXY`) in the shared VPC host project's network and region

## Setup

### 1. Create a GCP Service Account in the Host Project

```bash
export HOST_PROJECT=<your-host-project>
export SERVICE_PROJECT=<your-service-project>
export GCP_SA_NAME=gke-vpc-fwr-controller

gcloud iam service-accounts create $GCP_SA_NAME \
  --project=$HOST_PROJECT \
  --display-name="GKE VPC Firewall Rules Controller"
```

### 2. Grant IAM Roles

The GCP service account needs permissions in **both** the host project (to manage firewall rules and read subnets) and the service project (to read cluster info).

**On the shared VPC host project** — firewall management and subnet discovery:

```bash
# Firewall rule management
gcloud projects add-iam-policy-binding $HOST_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$HOST_PROJECT.iam.gserviceaccount.com" \
  --role="roles/compute.securityAdmin"

# Subnet discovery (to find proxy-only subnet)
gcloud projects add-iam-policy-binding $HOST_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$HOST_PROJECT.iam.gserviceaccount.com" \
  --role="roles/compute.networkViewer"
```

> **Least-privilege alternative:** Instead of `roles/compute.securityAdmin`, create a custom role with only:
> `compute.firewalls.create`, `compute.firewalls.delete`, `compute.firewalls.get`, `compute.firewalls.update`, `compute.networks.updatePolicy`

**On the service project** — cluster info discovery:

```bash
gcloud projects add-iam-policy-binding $SERVICE_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$HOST_PROJECT.iam.gserviceaccount.com" \
  --role="roles/container.clusterViewer"
```

### 3. Bind Workload Identity

Link the Kubernetes service account to the GCP service account so the controller pod can authenticate as the GCP SA:

```bash
gcloud iam service-accounts add-iam-policy-binding \
  $GCP_SA_NAME@$HOST_PROJECT.iam.gserviceaccount.com \
  --project=$HOST_PROJECT \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$SERVICE_PROJECT.svc.id.goog[gke-vpc-fwr-controller/gke-vpc-fwr-controller]"
```

The member format is `serviceAccount:<SERVICE_PROJECT>.svc.id.goog[<K8S_NAMESPACE>/<K8S_SA_NAME>]`.

### 4. Update the ServiceAccount Manifest

Edit `deploy/serviceaccount.yaml` and replace the annotation with your GCP service account:

```yaml
annotations:
  iam.gke.io/gcp-service-account: gke-vpc-fwr-controller@<HOST_PROJECT>.iam.gserviceaccount.com
```

### 5. Build and Push the Image

```bash
export IMG=<your-registry>/gke-vpc-fwr-rules-controller:latest

make docker-build IMG=$IMG
make docker-push IMG=$IMG
```

Update the image in `deploy/deployment.yaml`:

```yaml
image: <your-registry>/gke-vpc-fwr-rules-controller:latest
```

### 6. Deploy

```bash
make deploy
```

Or manually:

```bash
kubectl apply -f deploy/
```

## Configuration

The controller accepts the following command-line flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--sync-period` | `20m` | Periodic resync interval for detecting firewall rule drift |
| `--leader-elect` | `false` | Enable leader election (recommended for HA) |
| `--metrics-bind-address` | `:8080` | Metrics endpoint address |
| `--health-probe-bind-address` | `:8081` | Health/readiness probe endpoint address |

Example with custom sync period in `deploy/deployment.yaml`:

```yaml
args:
  - --leader-elect=true
  - --sync-period=10m
```

## IAM Permissions Summary

| Scope | Role | Purpose |
|-------|------|---------|
| Host project | `roles/compute.securityAdmin` | Create/update/delete firewall rules |
| Host project | `roles/compute.networkViewer` | List subnets to find proxy-only subnet |
| Service project | `roles/container.clusterViewer` | Read cluster network and pod CIDR |
| Host project | `roles/iam.workloadIdentityUser` | Allow K8s SA to act as GCP SA |

## Kubernetes RBAC

The controller requires cluster-wide read access to Gateway resources:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| `gateway.networking.k8s.io` | `gateways`, `gatewayclasses` | `get`, `list`, `watch` |
| `coordination.k8s.io` | `leases` | `get`, `create`, `update` |
| ` ` | `events` | `create`, `patch` |

## Uninstalling

To cleanly remove the controller and its firewall rule:

1. Delete all matching Gateway resources first (this triggers the controller to clean up the firewall rule).
2. Verify the firewall rule is gone:
   ```bash
   gcloud compute firewall-rules describe gke-<cluster-name>-gw-proxy-to-pods \
     --project=$HOST_PROJECT
   ```
3. Remove the controller:
   ```bash
   kubectl delete -f deploy/
   ```

If the controller is removed before the Gateways, the firewall rule will be orphaned. It can be manually deleted:

```bash
gcloud compute firewall-rules delete gke-<cluster-name>-gw-proxy-to-pods \
  --project=$HOST_PROJECT
```
