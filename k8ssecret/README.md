# hotload/k8ssecret

A [hotload](https://github.com/infobloxopen/hotload) strategy that watches Kubernetes Secrets for connection string changes via the K8s API.

Use this when the Secret containing your database credentials is in a **different namespace** than your application — where a volume mount isn't possible.

## Installation

```bash
go get github.com/infobloxopen/hotload/k8ssecret
```

This is a separate Go module. It pulls in `k8s.io/client-go` but does **not** add those dependencies to the root `hotload` module.

## Usage

```go
import (
    "database/sql"

    _ "github.com/infobloxopen/hotload"
    _ "github.com/infobloxopen/hotload/k8ssecret"
    _ "github.com/jackc/pgx/v5/stdlib" // or any sql driver
)

func main() {
    // The Secret "orders-db" in namespace "orders-ns" has a key "dsn.txt"
    // containing: postgres://statexfer:s3cret@orders-pg:5432/orders?sslmode=require
    db, err := sql.Open("hotload", "k8ssecret://pgx/orders-db?namespace=orders-ns&dsn=dsn.txt")
    if err != nil {
        log.Fatal(err)
    }
    db.Query("SELECT 1")
}
```

When the Secret is updated (e.g., credential rotation), hotload detects the change and transparently creates new connections with the updated DSN.

## DSN Format

```
k8ssecret://<driver>/<secret-name>?namespace=<ns>&dsn=<key>
```

| Component | Description |
|-----------|-------------|
| `<driver>` | The registered database driver name (e.g., `pgx`, `postgres`, `mysql`) |
| `<secret-name>` | Kubernetes Secret name |
| `namespace` | Namespace containing the Secret. Defaults to the pod's namespace (from service account mount) or `"default"`. |
| `dsn` | Data key within the Secret that holds the connection string. Defaults to `"dsn.txt"`. |

## Kubernetes Secret

The Secret holds connection strings as data keys. Each key is a file when volume-mounted, or a watchable field via this strategy:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: orders-db
  namespace: orders-ns
type: Opaque
stringData:
  dsn.txt: "postgres://statexfer:s3cret@orders-pg:5432/orders?sslmode=require"
```

The `dsn` parameter selects which key to read:

```
k8ssecret://pgx/orders-db?namespace=orders-ns&dsn=dsn.txt
```

## When to Use This vs fsnotify

| Scenario | Strategy | Why |
|----------|----------|-----|
| Secret in the **same** namespace | `fsnotify` | Mount the Secret as a volume. Cheaper — no API calls. |
| Secret in a **different** namespace | `k8ssecret` | Volume mounts can't cross namespaces. This strategy watches via the K8s API. |

For same-namespace Secrets mounted as volumes:

```go
// Secret mounted at /var/run/secrets/myapp/dsn.txt
db, err := sql.Open("hotload", "fsnotify://pgx/var/run/secrets/myapp/dsn.txt")
```

## RBAC

The service account running your application needs `get` and `watch` permissions on the target Secret:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: secret-reader
  namespace: orders-ns
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["orders-db"]
    verbs: ["get", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: statexd-reads-orders-db
  namespace: orders-ns
subjects:
  - kind: ServiceAccount
    name: statexd
    namespace: statexfer-system
roleRef:
  kind: Role
  name: secret-reader
  apiGroup: rbac.authorization.k8s.io
```

## How It Works

1. On first `Watch()` call, the strategy fetches the Secret via `Secrets(namespace).Get()`.
2. It reads the value of the specified `key` and returns it as the initial connection string.
3. A background goroutine establishes a K8s watch on the Secret.
4. When the Secret is modified, the new value is pushed to hotload, which transparently rotates connections.
5. If the watch is interrupted (API server restart, network partition), it reconnects automatically with a 2-second backoff.
