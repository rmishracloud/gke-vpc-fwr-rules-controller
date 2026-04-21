# GKE VPC Firewall Rules Controller

A Kubernetes controller that automatically creates VPC firewall rules when GKE Gateway resources are deployed in a shared VPC environment.

## Problem

When using GKE Gateway (internal or regional external Application Load Balancer) in a **shared VPC**, GKE does not automatically create the ingress firewall rules needed for proxy traffic to reach pods. Without these rules, traffic from the load balancer's proxy-only subnet is blocked from reaching the pod network.

## What It Does

The controller watches for Kubernetes `Gateway` resources with the following GatewayClasses:

- `gke-l7-rilb` (internal Application Load Balancer)
- `gke-l7-regional-external-managed` (regional external Application Load Balancer)

When such a Gateway exists, it automatically creates two VPC firewall rules:

**1. Proxy-only subnet rule** (`gke-<cluster-name>-gw-proxy-to-pods`) — created in the **host project**:

| Field | Value |
|-------|-------|
| Direction | `INGRESS` |
| Source ranges | Proxy-only subnet CIDR (`purpose=REGIONAL_MANAGED_PROXY`) |
| Destination ranges | Cluster pod CIDR |
| Protocol | TCP (all ports) |
| Priority | 1000 |

**2. Health check rule** (`gke-<cluster-name>-hc`) — created in the **host project**:

| Field | Value |
|-------|-------|
| Direction | `INGRESS` |
| Source ranges | `35.191.0.0/16`, `130.211.0.0/22` (Google health check ranges) |
| Destination ranges | Cluster pod CIDR |
| Protocol | TCP (all ports) |
| Priority | 1000 |

Both rules are fully idempotent — created, updated, or removed based on the presence of matching Gateway resources.

When all matching Gateways are deleted, both firewall rules are cleaned up automatically.

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
│                                                         │
│  ┌────────────────────────────────────────────────────┐ │
│  │ Firewall Rule (Health Check)                       │ │
│  │ gke-<cluster>-hc                  │ │
│  │ Source: 35.191.0.0/16, 130.211.0.0/22              │ │
│  └────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

### Reconciliation Flow

1. On any Gateway create/update/delete event (or periodic resync), the controller lists all Gateways across all namespaces.
2. Filters to those matching the supported GatewayClasses.
3. If any matching Gateways exist: discovers the proxy-only subnet CIDR and ensures both firewall rules (proxy-only subnet and health check) are present and correct.
4. If no matching Gateways exist: deletes both firewall rules.
5. Periodic resync (default: 20 minutes) ensures the firewall rule is self-healing even if modified or deleted out-of-band.

### Discovery

At startup, the controller reads its identity from the **GCP metadata server** (project, zone, cluster name), then calls the **GKE Container API** to resolve the VPC network, pod CIDR, and shared VPC host project. The proxy-only subnet is discovered via the **Compute Subnets API** and cached for 5 minutes.

## Multi-Cluster Behavior

The controller is designed to run independently in each GKE cluster and coexist safely with other instances managing the same shared VPC host project:

- **Per-cluster ownership via naming**: Firewall rules are prefixed with the cluster name (`gke-<cluster-name>-gw-proxy-to-pods` and `gke-<cluster-name>-hc`), so each controller instance only touches its own rules.
- **Scoped destination ranges**: Each rule's destination is restricted to that cluster's own pod CIDR, so the blast radius is limited to a single cluster even though the proxy-only subnet source is shared across all clusters in a region.
- **Regional proxy subnet discovery**: Each controller discovers the proxy-only subnet for its own region and network, so clusters in different regions of the same VPC each get the correct regional source range.
- **Net result**: N clusters in the same shared VPC will produce 2N firewall rules in the host project — two per cluster — all sharing the regional proxy-only subnet as source but each scoped to a unique pod CIDR.

> **Caveat — duplicate cluster names**: If two clusters share the same name (GKE permits this across regions or projects), their controllers will generate identical firewall rule names and fight over ownership. Ensure cluster names are unique across any clusters that share the same VPC host project, or fork the controller to include the region in the rule name.

## Prerequisites

- GKE cluster in a **shared VPC** with Workload Identity enabled
- Gateway API CRDs installed on the cluster (`gateway.networking.k8s.io/v1`)
- A proxy-only subnet (`purpose=REGIONAL_MANAGED_PROXY`) in the shared VPC host project's network and region

## Setup

### 1. Create a GCP Service Account in the Service Project

The service account lives in the **service project** (the same project as the GKE cluster). This keeps identity management with the team that owns the cluster while still granting the SA explicit, scoped permissions to manage firewall rules on the shared VPC in the host project.

```bash
export HOST_PROJECT=<your-host-project>
export SERVICE_PROJECT=<your-service-project>
export GCP_SA_NAME=gke-vpc-fwr-controller

gcloud iam service-accounts create $GCP_SA_NAME \
  --project=$SERVICE_PROJECT \
  --display-name="GKE VPC Firewall Rules Controller"
```

The SA's fully-qualified email is `$GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com`. Note that it lives in the service project but will be granted permissions on resources in the host project.

### 2. Grant IAM Roles

The service-project SA needs permissions in **both** the host project (to manage firewall rules and read subnets on the shared VPC) and the service project (to read its own cluster info).

**On the shared VPC host project** — firewall management and subnet discovery:

```bash
# Firewall rule management
gcloud projects add-iam-policy-binding $HOST_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com" \
  --role="roles/compute.securityAdmin"

# Subnet discovery (to find proxy-only subnet)
gcloud projects add-iam-policy-binding $HOST_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com" \
  --role="roles/compute.networkViewer"
```

> **Least-privilege alternative:** Instead of `roles/compute.securityAdmin`, create a custom role in the host project with only:
> `compute.firewalls.create`, `compute.firewalls.delete`, `compute.firewalls.get`, `compute.firewalls.update`, `compute.networks.updatePolicy`
>
> GCP legacy VPC firewall rules are a project-level resource, so the role must be granted at the host project level — there is no VPC-resource-level binding available for `compute.firewalls.*`. The custom role keeps the verb set minimal.

**Tightening blast radius with IAM Conditions:**

Although the role binding is project-scoped, you can attach an **IAM Condition** so it only applies to firewall rules the controller owns (names starting with `gke-<cluster-name>-`). This prevents the SA from touching any other firewall rule in the host project:

```bash
export CLUSTER_NAME=<your-gke-cluster-name>

gcloud projects add-iam-policy-binding $HOST_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com" \
  --role="roles/compute.securityAdmin" \
  --condition="expression=resource.name.startsWith('projects/$HOST_PROJECT/global/firewalls/gke-$CLUSTER_NAME-'),title=only-this-clusters-firewalls,description=Restrict to firewall rules managed by this cluster's controller"
```

With this condition, the SA can only act on `gke-<cluster>-gw-proxy-to-pods` and `gke-<cluster>-hc` — and nothing else in the host project, even though the role itself is project-scoped.

> **Note:** `compute.networkViewer` cannot be restricted this way because the controller needs to `List()` subnets across the region to discover the proxy-only subnet, and list operations don't have a per-resource `resource.name` to condition on. Keep it as a plain project-level binding, or grant only `compute.subnetworks.list` via a custom role for a slightly smaller surface.

**Alternative — Network Firewall Policies:** GCP's newer Network Firewall Policy API supports true resource-level IAM on each policy, meaning you could grant the controller's SA permissions on a single policy resource rather than the whole project. That would require changing the controller to manage policy rules instead of VPC firewall rules — a non-trivial rewrite not currently implemented.

**On the service project** — cluster info discovery:

```bash
gcloud projects add-iam-policy-binding $SERVICE_PROJECT \
  --member="serviceAccount:$GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com" \
  --role="roles/container.clusterViewer"
```

### 3. Bind Workload Identity

Link the Kubernetes service account to the GCP service account so the controller pod can authenticate as the GCP SA. Because the GCP SA now lives in the service project (same project as the cluster), the Workload Identity binding is entirely within the service project:

```bash
gcloud iam service-accounts add-iam-policy-binding \
  $GCP_SA_NAME@$SERVICE_PROJECT.iam.gserviceaccount.com \
  --project=$SERVICE_PROJECT \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$SERVICE_PROJECT.svc.id.goog[gke-vpc-fwr-controller/gke-vpc-fwr-controller]"
```

The member format is `serviceAccount:<SERVICE_PROJECT>.svc.id.goog[<K8S_NAMESPACE>/<K8S_SA_NAME>]`.

### 4. Update the ServiceAccount Manifest

Edit `deploy/serviceaccount.yaml` and replace the annotation with your GCP service account:

```yaml
annotations:
  iam.gke.io/gcp-service-account: gke-vpc-fwr-controller@<SERVICE_PROJECT>.iam.gserviceaccount.com
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

The GCP service account lives in the **service project** and is granted the following roles. `roles/iam.workloadIdentityUser` is bound on the SA itself (in the service project), not at a project level.

| Granted On | Role | Purpose |
|------------|------|---------|
| Host project | `roles/compute.securityAdmin` | Create/update/delete firewall rules (proxy-only subnet and health check) |
| Host project | `roles/compute.networkViewer` | List subnets to find proxy-only subnet |
| Service project | `roles/container.clusterViewer` | Read cluster network and pod CIDR |
| Service-project SA | `roles/iam.workloadIdentityUser` | Allow the K8s SA to impersonate the GCP SA |

## Kubernetes RBAC

The controller requires cluster-wide read access to Gateway resources:

| API Group | Resources | Verbs |
|-----------|-----------|-------|
| `gateway.networking.k8s.io` | `gateways`, `gatewayclasses` | `get`, `list`, `watch` |
| `coordination.k8s.io` | `leases` | `get`, `create`, `update` |
| ` ` | `events` | `create`, `patch` |

## Uninstalling

To cleanly remove the controller and its firewall rules:

1. Delete all matching Gateway resources first (this triggers the controller to clean up both firewall rules).
2. Verify the firewall rules are gone:
   ```bash
   gcloud compute firewall-rules describe gke-<cluster-name>-gw-proxy-to-pods \
     --project=$HOST_PROJECT
   gcloud compute firewall-rules describe gke-<cluster-name>-gw-proxy-to-pods-hc \
     --project=$HOST_PROJECT
   ```
3. Remove the controller:
   ```bash
   kubectl delete -f deploy/
   ```

If the controller is removed before the Gateways, the firewall rules will be orphaned. They can be manually deleted:

```bash
gcloud compute firewall-rules delete gke-<cluster-name>-gw-proxy-to-pods \
  --project=$HOST_PROJECT
gcloud compute firewall-rules delete gke-<cluster-name>-hc \
  --project=$HOST_PROJECT
```
