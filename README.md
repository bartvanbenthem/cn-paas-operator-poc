# paas-operator

A meta operator, built with [kubebuilder](https://book.kubebuilder.io)/
`controller-runtime`, that fronts other operators' large CRDs with small,
opinionated ones of our own. Each vendor gets its own thin CRD under
`paas.example.com/v1alpha1` — currently `PostgresCluster` (for
[CloudNativePG](https://cloudnative-pg.io)), `ValkeyCluster` (for
[valkey-io/valkey-operator](https://github.com/valkey-io/valkey-operator)),
`GrafanaInstance` (for
[grafana/grafana-operator](https://github.com/grafana/grafana-operator)),
`MariaDBCluster` (for
[mariadb-operator](https://github.com/mariadb-operator/mariadb-operator)),
`RabbitMQCluster` (for the [RabbitMQ Cluster
Operator](https://github.com/rabbitmq/cluster-operator)),
`PrometheusInstance` (for the [Prometheus
Operator](https://github.com/prometheus-operator/prometheus-operator)),
`MongoDBCluster` (for the [Percona Server for MongoDB
Operator](https://github.com/percona/percona-server-mongodb-operator)),
`KafkaCluster` (for the [Strimzi Kafka
Operator](https://github.com/strimzi/strimzi-kafka-operator)), and
`LokiInstance` (for the [Loki
Operator](https://github.com/grafana/loki/tree/main/operator)) —
reconciled into a full vendor object, so consumers get a working
Postgres/Valkey/Grafana/MariaDB/RabbitMQ/Prometheus/MongoDB/Kafka/Loki
instance from ~10 lines of YAML instead of having to understand the vendor's
much larger spec.

```yaml
apiVersion: paas.example.com/v1alpha1
kind: PostgresCluster
metadata:
  name: example-db
spec:
  instances: 3
  storage:
    size: 10Gi
  database:
    name: app
    owner: app
---
apiVersion: paas.example.com/v1alpha1
kind: ValkeyCluster
metadata:
  name: example-cache
spec:
  shards: 3
  replicas: 1
  persistence:
    size: 5Gi
---
apiVersion: paas.example.com/v1alpha1
kind: GrafanaInstance
metadata:
  name: example-grafana
spec:
  replicas: 1
  persistence:
    size: 1Gi
---
apiVersion: paas.example.com/v1alpha1
kind: MariaDBCluster
metadata:
  name: example-mariadb
spec:
  replicas: 3
  storage:
    size: 10Gi
  database:
    name: app
    owner: app
---
apiVersion: paas.example.com/v1alpha1
kind: RabbitMQCluster
metadata:
  name: example-rabbitmq
spec:
  replicas: 3
  storage:
    size: 10Gi
---
apiVersion: paas.example.com/v1alpha1
kind: PrometheusInstance
metadata:
  name: example-prometheus
spec:
  replicas: 1
  retention: 15d
  storage:
    size: 10Gi
---
apiVersion: paas.example.com/v1alpha1
kind: MongoDBCluster
metadata:
  name: example-mongodb
spec:
  replicas: 3
  storage:
    size: 10Gi
---
apiVersion: paas.example.com/v1alpha1
kind: KafkaCluster
metadata:
  name: example-kafka
spec:
  replicas: 3
  storage:
    size: 100Gi
---
apiVersion: paas.example.com/v1alpha1
kind: LokiInstance
metadata:
  name: example-loki
spec:
  size: 1x.demo
  storageClassName: standard
  objectStorage:
    secretName: example-loki-s3
```

## How it works

Every vendor integration follows the same shape: our own thin CRD, a mapping
onto the vendor's real object, and a controller. The mapping and controller
are split into a reusable engine plus a small per-vendor adapter, so adding a
type means writing the adapter, not another copy of the reconcile loop.

- `internal/reconciler/reconciler.go` — the shared engine
  (`GenericReconciler[T, PT]`) used by every vendor integration:
  finalizer-gated cleanup, idempotent Server-Side Apply of the vendor object,
  status-mirroring with a standard `Ready` condition, and event recording.
  It's generic over the paas CR type and driven by a small `Adapter`
  interface — this is the one place the actual CRUD/reconciliation logic
  lives.
- `internal/cnpg/cnpg.go`, `internal/valkey/valkey.go`,
  `internal/grafana/grafana.go`, `internal/mariadb/mariadb.go`,
  `internal/rabbitmq/rabbitmq.go`, `internal/prometheus/prometheus.go`,
  `internal/psmdb/psmdb.go`, `internal/strimzi/strimzi.go`, and
  `internal/loki/loki.go` — one `Adapter` implementation per vendor object:
  the target `GroupVersionKind`, how to build the desired vendor object from
  our CR's spec, and how to read phase/readiness back out of its status.
  None of the vendors' Go API types or CRD schemas are vendored — we don't
  own those CRDs, and their schemas are large and version-specific (see
  `crd-cnpg-v1.30.0.yaml`, `crd-valkey-v0.6.0.yaml`,
  `crd-grafana-v5.25.0.yaml`, `crd-mariadb-operator-v26.6.0.yaml`,
  `crd-rabbitmq-cluster-operator-v2.22.5.yaml`,
  `crd-prometheus-operator-v0.93.1.yaml`,
  `crd-percona-server-mongodb-operator-v1.23.0.yaml`,
  `crd-strimzi-kafka-operator-v1.2.0.yaml`, and
  `crd-loki-operator-v0.11.0.yaml`, all under `external-crds/`).
  Instead the target is addressed purely through controller-runtime's
  dynamic client (`unstructured.Unstructured` + a `schema.GroupVersionKind`),
  and the desired object is built as a plain `map[string]any` applied via
  Server-Side Apply (`client.Patch(ctx, obj, client.Apply, ...)`). This keeps
  the operator decoupled from any one vendor version.
- `api/v1alpha1/postgrescluster_types.go` / `valkeycluster_types.go` /
  `grafanainstance_types.go` / `mariadbcluster_types.go` /
  `rabbitmqcluster_types.go` / `prometheusinstance_types.go` /
  `mongodbcluster_types.go` / `kafkacluster_types.go` /
  `lokiinstance_types.go` — our own CRDs, scaffolded and generated the
  normal kubebuilder way (`+kubebuilder:validation`/`+kubebuilder:printcolumn`
  markers, `controller-gen` for the CRD YAML and deepcopy code).
- `internal/controller/postgrescluster_controller.go` /
  `valkeycluster_controller.go` / `grafanainstance_controller.go` /
  `mariadbcluster_controller.go` / `rabbitmqcluster_controller.go` /
  `prometheusinstance_controller.go` / `mongodbcluster_controller.go` /
  `kafkacluster_controller.go` / `lokiinstance_controller.go` — a few lines
  each: they just instantiate `GenericReconciler` with the matching adapter
  (`cnpg.Adapter{}` / `valkey.Adapter{}` / `grafana.Adapter{}` /
  `mariadb.Adapter{}` / `rabbitmq.Adapter{}` / `prometheus.Adapter{}` /
  `psmdb.Adapter{}` / `strimzi.Adapter{}` / `loki.Adapter{}`) and register it
  with the manager. RBAC markers for both our own CRD and the vendor's live
  here.
- One paas CR maps to exactly one same-named vendor object (`KafkaCluster` is
  the one exception with two: see its own note in Scope below). On delete,
  the operator removes the vendor object before releasing its own finalizer,
  so e.g. `kubectl delete postgrescluster` also tears down the database.
- Status is refreshed by polling (15s while not ready, 5m once ready) rather
  than a push watch: `unstructured.Unstructured` isn't registered with the
  manager's scheme, so an `Owns()`-style watch isn't available the way it is
  for typed children. Wiring a raw `source.Kind` watch on the dynamic GVK
  would remove the poll delay, if it's ever worth the complexity.
- `internal/controller/testdata/crd/postgresql.cnpg.io_clusters.yaml`,
  `valkey.io_valkeyclusters.yaml`, `grafana.integreatly.org_grafanas.yaml`,
  `k8s.mariadb.com_mariadbs.yaml`, `rabbitmq.com_rabbitmqclusters.yaml`,
  `monitoring.coreos.com_prometheuses.yaml`,
  `psmdb.percona.com_perconaservermongodbs.yaml`,
  `kafka.strimzi.io_kafkas.yaml` / `kafka.strimzi.io_kafkanodepools.yaml`,
  and `loki.grafana.com_lokistacks.yaml` are (trimmed copies of, for the
  last three) the real vendor CRDs, loaded into `envtest` so the controller
  tests validate the generated objects against each vendor's actual OpenAPI
  schema — not just against our own assumptions about its shape.

### Adding another vendor integration

1. `kubebuilder create api --group paas --version v1alpha1 --kind <Kind>` for
   the thin CRD, then trim `<Kind>Spec`/`<Kind>Status` to the handful of
   fields worth exposing (see Scope below).
2. Add `internal/<vendor>/<vendor>.go` implementing
   `reconciler.Adapter[paasv1alpha1.<Kind>, *paasv1alpha1.<Kind>]` — GVK,
   `BuildManifest`, `ExtractStatus`, `ApplyStatus` (see `internal/cnpg` or
   `internal/valkey` as a template).
3. Replace the scaffolded controller body with a thin wrapper that builds
   `reconciler.GenericReconciler[...]{Adapter: <vendor>.Adapter{}, ...}` (see
   `internal/controller/valkeycluster_controller.go`), and wire it up in
   `cmd/main.go`.
4. Drop the vendor's real CRD YAML into `internal/controller/testdata/crd/`
   for envtest, and `make manifests generate`.

Rust/`kube-rs` was evaluated for this (a parallel implementation lived in
`rust-koprs/`) and dropped: the unstructured-client + Server-Side Apply
approach that keeps this operator decoupled from vendor CRD versions is
available identically in kube-rs, so a second language bought nothing beyond
a second toolchain to maintain.

## Scope

Deliberately minimal, per type. `PostgresCluster` exposes `instances`,
`image`, `storage.size`, `storage.storageClass`, `database.name`,
`database.owner`, and optional `resources`; `ValkeyCluster` exposes `shards`,
`replicas`, `image`, `persistence.size`, `persistence.storageClass`, and
optional `resources`; `GrafanaInstance` exposes `version`, `replicas`, and
optional `persistence.size`/`persistence.storageClass` (no PVC is requested
when `persistence` is unset — Grafana runs with ephemeral storage);
`MariaDBCluster` exposes `replicas`, `image`, `storage.size`,
`storage.storageClass`, `database.name`, `database.owner`, and optional
`resources` (`replicas` > 1 also turns on Galera Cluster, mariadb-operator's
synchronous multi-primary replication); `RabbitMQCluster` exposes `replicas`,
`image`, `storage.size`, `storage.storageClass`, and optional `resources`
(no bootstrap-database equivalent — the RabbitMQ Cluster Operator always
creates a default vhost and user itself, writing credentials to a
`<name>-default-user` Secret it manages); `PrometheusInstance` exposes
`version`, `replicas`, `retention`, optional `storage.size`/
`storage.storageClass` (no PVC is requested when `storage` is unset —
Prometheus runs with ephemeral storage), optional `resources`, and optional
`ingress` (the Prometheus Operator creates no Service of its own for
Prometheus, so setting `ingress` also makes this operator create a
ClusterIP Service fronting it); `MongoDBCluster` exposes `replicas`, `image`,
`storage.size`, `storage.storageClass`, optional `resources`, optional
`monitoring.enablePodMonitor`, and optional `expose`; `KafkaCluster` exposes
`replicas`, `version`, `storage.size`, `storage.storageClass`, optional
`resources`, optional `monitoring.enablePodMonitor`, and optional `expose`;
`LokiInstance` exposes `size`, `storageClassName`, and
`objectStorage.secretName`. `GrafanaInstance` additionally exposes an
optional `lokiRef`, mirroring its required `prometheusRef`: when set,
`internal/grafana` creates a second GrafanaDatasource wiring the Grafana to
the named `LokiInstance`, the same way `prometheusRef` wires it to a
PrometheusInstance.
All `resources` fields reuse `corev1.ResourceRequirements` directly, except
`GrafanaInstance`, which doesn't expose one: the real field lives deep
inside `spec.deployment.spec.template.spec.containers[].resources` (keyed by
container name) rather than as a simple top-level object, so it was left out
of this first pass rather than guessing at the container name grafana-operator
expects. Everything else in the generated vendor object (CNPG's backups,
monitoring, affinity, pooling, superuser secret, etc.; Valkey's TLS, ACLs,
exporter, pod disruption budget, etc.; Grafana's ingress/route, SMTP,
plugins, jsonnet, service accounts, etc.; mariadb-operator's TLS,
replication (non-Galera), MaxScale, backups, etc.; the RabbitMQ Cluster
Operator's TLS, plugins, definitions import, affinity, etc.; the Prometheus
Operator's rule/scrape-config selectors, remote write, Alertmanager
discovery, affinity, etc.; the Percona Server for MongoDB Operator's
sharding, backups, PMM, custom users, TLS, etc.; the Strimzi Kafka
Operator's `Kafka.spec.kafka.config` broker tuning, Cruise Control, Kafka
Connect/MirrorMaker/Bridge, entity operator, dedicated controller/broker
node pools, etc.; the Loki Operator's `spec.tenants`, `spec.rules`,
`spec.limits`, `spec.replicationFactor`, per-component `spec.template`
overrides, etc.) is left at that vendor's own defaults. Extending a mapping
means adding a field to the CR's `*Spec` type in `api/v1alpha1/`, running
`make manifests generate`, and threading it through that vendor's
`BuildManifest` in `internal/<vendor>/`.

`MongoDBCluster` and `KafkaCluster` also diverge from the rest in shape, not
just field count:
- `MongoDBCluster` has no `database.name`/`database.owner` fields the way
  `PostgresCluster`/`MariaDBCluster` do — MongoDB has no bootstrap-database-
  with-an-owning-role concept to bootstrap (databases/collections are
  created implicitly on first write).
- `KafkaCluster` maps to **two** vendor objects, not one: the Strimzi Kafka
  Operator is KRaft-only (ZooKeeper has been fully removed) and a `Kafka`
  does nothing without at least one `KafkaNodePool` supplying its controller
  and broker nodes. `KafkaCluster`'s single `replicas` field sizes one
  combined controller+broker `KafkaNodePool`, which `internal/strimzi`
  always creates alongside the `Kafka` (unconditionally, not gated on
  monitoring) — Strimzi's own docs call a combined pool valid and the right
  shape for small/dev clusters (roughly up to 5 brokers); dedicated
  controller/broker pools are out of scope. `KafkaClusterStatus` also has no
  replica-count field, unlike every other `*ClusterStatus` here — Strimzi's
  `Kafka.status` itself has none (only `status.conditions[]`), so readiness
  is derived purely from its `Ready` condition.
- `MongoDBCluster`'s and `KafkaCluster`'s vendor operators (Percona Server
  for MongoDB Operator, Strimzi) have no native PodMonitor/ServiceMonitor of
  their own the way CNPG/mariadb-operator do — `monitoring.enablePodMonitor`
  on these two instead makes `internal/psmdb`/`internal/strimzi` build a
  `PodMonitor` directly (the same workaround `internal/valkey` uses for
  valkey-operator). For `MongoDBCluster` this also injects a
  `percona/mongodb_exporter` sidecar into the replica set pod, using
  credentials from the operator's own auto-generated `clusterMonitor`
  system user; for `KafkaCluster` it instead turns on the modern Strimzi
  Metrics Reporter (`spec.kafka.metricsConfig.type: strimziMetricsReporter`,
  a native Kafka plugin, not a sidecar) — Percona's/Strimzi's own PMM/JMX
  based monitoring stacks are not used, since every other building block
  here standardizes on the Prometheus Operator + Grafana instead.
- `LokiInstance` diverges the most of all: it has no ephemeral/local-disk
  storage mode at all (`objectStorage.secretName` and `storageClassName`
  are both required, not optional the way `storage`/`persistence` are
  elsewhere), it's sized by a `size` t-shirt value instead of a `replicas`
  count, and like `KafkaClusterStatus`, `LokiInstanceStatus` has no
  replica-count field — the underlying LokiStack's own `.status` has none
  (only `status.conditions[]`, `status.components`, and `status.storage`),
  so readiness is derived purely from its `Ready` condition. Only
  S3-compatible object storage is supported (see `LokiInstanceSpec`'s doc
  comment) — GCS/Azure/Swift/AlibabaCloud are out of scope.

Notes on what's deliberately out of scope:
- valkey-operator also defines a `ValkeyNode` CRD, but it's explicitly
  internal to that operator ("users should not create ValkeyNodes directly"
  per its own CRD description) — this operator only targets `ValkeyCluster`.
- grafana-operator ships a large family of child CRDs (`GrafanaDashboard`,
  `GrafanaDataSource`, `GrafanaFolder`, `GrafanaAlertRuleGroup`, ...) that
  configure a running Grafana — this operator only targets `Grafana` itself
  (the actual instance), the same way it only targets CNPG's `Cluster` and
  not CNPG's `Pooler`/`Backup`.
- mariadb-operator also defines `Backup`/`Restore`/`Connection`/`Database`/
  `User`/`Grant`/`MaxScale` CRDs; this operator only targets `MariaDB`
  itself, the actual cluster.
- the Prometheus Operator also defines `Alertmanager`/`ThanosRuler`/
  `PrometheusAgent`/`ServiceMonitor`/`PodMonitor`/`Probe`/`PrometheusRule`
  CRDs; this operator only targets `Prometheus` itself, the actual server —
  `ServiceMonitor`/`PodMonitor` objects are expected to be created
  separately (e.g. by CNPG's or mariadb-operator's own `MonitoringSpec`) and
  are simply discovered via the empty selectors `BuildManifest` sets.
- the Percona Server for MongoDB Operator also defines
  `PerconaServerMongoDBBackup`/`PerconaServerMongoDBRestore`/
  `PerconaServerMongoDBClusterSync` CRDs; this operator only targets
  `PerconaServerMongoDB` itself, the actual cluster. Sharded topologies
  (multiple replica sets, `mongos`) are also out of scope — `MongoDBCluster`
  always creates a single, non-sharded replica set named `rs0`.
- the Strimzi Kafka Operator also defines `KafkaTopic`/`KafkaUser`/
  `KafkaConnect`/`KafkaConnector`/`KafkaMirrorMaker2`/`KafkaBridge`/
  `KafkaRebalance` CRDs (and Strimzi's own `entityOperator`, Cruise Control,
  Kafka Connect); this operator only targets `Kafka` (plus the one
  `KafkaNodePool` it requires, see above), not any of the surrounding
  ecosystem.
- the Loki Operator also defines `AlertingRule`/`RecordingRule`/
  `RulerConfig` CRDs (all consumed by its optional ruler component, which
  `LokiInstance` never enables via `spec.rules`); this operator only
  targets `LokiStack` itself. Its multi-tenant gateway component is also
  out of scope, since it requires either OpenShift's own OAuth integration
  or a self-managed OIDC provider — see `internal/loki`'s package doc for
  what talking to a gatewayless LokiStack means for callers.

## Verified while building this

`go build`, `go vet`, `make manifests`/`generate` (regenerates CRD YAML +
deepcopy code via `controller-gen`), `make lint` (golangci-lint, 0 issues),
and `make test` (a real `envtest` API server, with our CRDs and CNPG's,
valkey-operator's, grafana-operator's, mariadb-operator's, the RabbitMQ
Cluster Operator's, the Prometheus Operator's, the Percona Server for
MongoDB Operator's, the Strimzi Kafka Operator's, and the Loki Operator's
actual (trimmed, for the last three) CRDs all loaded — see above) all pass;
all nine controller tests exercise finalizer-add, Server-Side Apply of the
generated vendor object(s), and the status-mirroring logic end to end
(`KafkaCluster`'s two tests also assert the `KafkaNodePool` extra is always
present regardless of the monitoring toggle). Not run: anything against a
live cluster with CNPG, valkey-operator, grafana-operator, mariadb-operator,
the RabbitMQ Cluster Operator, the Prometheus Operator, the Percona Server
for MongoDB Operator, the Strimzi Kafka Operator, or the Loki Operator
actually installed and reconciling — do that before trusting this in
anything real (the Grafana mapping in particular is untested against a live
grafana-operator: the `deployment.spec.replicas` and
`persistentVolumeClaim` paths are correct per its CRD schema, but its
actual reconciliation behavior hasn't been exercised end to end; same
caveat for `MariaDBCluster`/`RabbitMQCluster`/`PrometheusInstance`/
`MongoDBCluster`/`KafkaCluster`/`LokiInstance` against a live
mariadb-operator/RabbitMQ Cluster Operator/Prometheus Operator/Percona
Server for MongoDB Operator/Strimzi Kafka Operator/Loki Operator —
`MongoDBCluster`'s exporter-sidecar wiring and `KafkaCluster`'s
`KafkaNodePool` linkage are the two mappings with the most moving parts of
any building block here, so double-check `Ready` actually flips true, the
`PodMonitor` picks up real scrape targets, and the Grafana dashboards
render real panel data before relying on them; for `LokiInstance`
specifically, double-check that a real object storage Secret's key layout
matches what `internal/loki`'s doc comment assumes, that `Ready` actually
flips true for a real bucket, and that Grafana's auto-wired Loki datasource
can actually reach the query-frontend Service and return real query
results, since the gatewayless single-tenant request path it relies on has
not been exercised against a live Loki Operator).

## Installing the Dependency Operators

paas-operator only ever talks to the vendor CRDs (CNPG's `Cluster`,
valkey-operator's `ValkeyCluster`, grafana-operator's `Grafana`,
mariadb-operator's `MariaDB`, the RabbitMQ Cluster Operator's
`RabbitmqCluster`, the Prometheus Operator's `Prometheus`, the Percona
Server for MongoDB Operator's `PerconaServerMongoDB`, the Strimzi Kafka
Operator's `Kafka`/`KafkaNodePool`, and the Loki Operator's `LokiStack`) via
Server-Side Apply — it never installs the vendor operators themselves. Each
one has to be running in the cluster *before*
paas-operator can reconcile anything, otherwise `BuildManifest`'s
Server-Side Apply calls fail because the target CRD doesn't exist yet. Most
ship a Helm chart; the RabbitMQ Cluster Operator ships a plain manifest
instead (its own recommended install path). That's the preferred route for
each below. The Loki Operator is the one exception with no Helm chart and a
non-standard kustomize layout, plus an extra cert-manager prerequisite —
see its own section below.

Install order doesn't matter between them (none depend on each other) — just
make sure whichever ones you actually plan to create CRs for are up before
applying `config/samples/` or your own CRs.

### CloudNativePG (for `PostgresCluster`)

```sh
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm repo update
helm upgrade --install cnpg cnpg/cloudnative-pg \
  --version 1.30.0 -n cnpg-system --create-namespace
```

Verify:

```sh
kubectl get pods -n cnpg-system
kubectl get crd clusters.postgresql.cnpg.io
```

Manifest fallback (no Helm):

```sh
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.30/releases/cnpg-1.30.0.yaml
```

`external-crds/crd-cnpg-v1.30.0.yaml` is the CRD bundle this operator was
built and tested against — both routes above install the matching 1.30.0
release.

### valkey-operator (for `ValkeyCluster`)

```sh
helm repo add valkey https://valkey.io/valkey-helm
helm repo update
helm install valkey-operator valkey/valkey-operator \
  --version 0.6.0 -n valkey-operator-system --create-namespace
```

Verify:

```sh
kubectl get pods -n valkey-operator-system
kubectl get crd valkeyclusters.valkey.io
```

Manifest fallback (no Helm): valkey-operator doesn't publish a bundled
`install.yaml`; deploy from source instead —
`kubectl apply -k github.com/valkey-io/valkey-operator/config/default?ref=v0.6.0`.
`external-crds/crd-valkey-v0.6.0.yaml` is the CRD this operator was built
and tested against — the Helm chart above installs the matching `v0.6.0`
appVersion.

### grafana-operator (for `GrafanaInstance`)

```sh
helm upgrade -i grafana-operator \
  oci://ghcr.io/grafana/helm-charts/grafana-operator \
  --version 5.25.0 -n grafana-operator-system --create-namespace
```

Verify:

```sh
kubectl get pods -n grafana-operator-system
kubectl get crd grafanas.grafana.integreatly.org
```

Manifest fallback (no Helm): see the [grafana-operator Kustomize
guide](https://grafana.github.io/grafana-operator/docs/installation/kustomize/),
or `kubectl apply -k github.com/grafana/grafana-operator/config/default?ref=v5.25.0`.
`external-crds/crd-grafana-v5.25.0.yaml` is the CRD this operator was
built and tested against — the Helm chart above installs the matching
`5.25.0` version.

### mariadb-operator (for `MariaDBCluster`)

```sh
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator
helm repo update
helm install mariadb-operator-crds mariadb-operator/mariadb-operator-crds \
  --version 26.6.0
helm install mariadb-operator mariadb-operator/mariadb-operator \
  --version 26.6.0 -n mariadb-operator-system --create-namespace
```

Verify:

```sh
kubectl get pods -n mariadb-operator-system
kubectl get crd mariadbs.k8s.mariadb.com
```

Manifest fallback (no Helm): mariadb-operator doesn't publish a bundled
`install.yaml`; the Helm charts (also available as OCI images, see the
[Helm doc](https://github.com/mariadb-operator/mariadb-operator/blob/main/docs/helm.md))
are the supported install path.
`external-crds/crd-mariadb-operator-v26.6.0.yaml` is the CRD this operator
was built and tested against — the Helm
charts above install the matching `26.6.0` release.

### RabbitMQ Cluster Operator (for `RabbitMQCluster`)

```sh
kubectl apply -f \
  https://github.com/rabbitmq/cluster-operator/releases/download/v2.22.5/cluster-operator.yml
```

Verify:

```sh
kubectl get pods -n rabbitmq-system
kubectl get crd rabbitmqclusters.rabbitmq.com
```

The RabbitMQ Cluster Operator doesn't publish an official Helm chart — the
manifest above (installing into its own `rabbitmq-system` namespace) is the
project's own recommended install path.
`external-crds/crd-rabbitmq-cluster-operator-v2.22.5.yaml` is the CRD
this operator was built and tested against — the manifest above installs
the matching `2.22.5` release.

### Prometheus Operator (for `PrometheusInstance`)

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
helm upgrade --install prometheus-operator prometheus-community/kube-prometheus-stack \
  --version 89.2.0 -n prometheus-operator-system --create-namespace \
  --set prometheus.enabled=false \
  --set alertmanager.enabled=false \
  --set grafana.enabled=false \
  --set kubeStateMetrics.enabled=false \
  --set nodeExporter.enabled=false
```

`kube-prometheus-stack` is the community-maintained chart for the
Prometheus Operator itself — there's no separate lean "operator-only"
chart — so the flags above turn off everything this operator doesn't need
(the stack's own default Prometheus/Alertmanager, Grafana, and the
exporters), leaving just the operator and its CRDs.

Verify:

```sh
kubectl get pods -n prometheus-operator-system
kubectl get crd prometheuses.monitoring.coreos.com
```

Manifest fallback (no Helm):

```sh
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.93.1/bundle.yaml
```

`external-crds/crd-prometheus-operator-v0.93.1.yaml` is the CRD this
operator was built and tested against — both routes above install the
matching `0.93.1` release (`kube-prometheus-stack` `89.2.0` pins
`appVersion: v0.93.1`).

### Percona Server for MongoDB Operator (for `MongoDBCluster`)

Percona ships CRDs and operator as two separate charts — install both:

```sh
helm repo add percona https://percona.github.io/percona-helm-charts/
helm repo update
helm install psmdb-operator-crds percona/psmdb-operator-crds \
  --version 1.23.0 -n psmdb-operator-system --create-namespace
helm install psmdb-operator percona/psmdb-operator \
  --version 1.23.0 -n psmdb-operator-system
```

Verify:

```sh
kubectl get pods -n psmdb-operator-system
kubectl get crd perconaservermongodbs.psmdb.percona.com
```

Manifest fallback (no Helm):

```sh
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/v1.23.0/deploy/bundle.yaml
```

`external-crds/crd-percona-server-mongodb-operator-v1.23.0.yaml` is the CRD
bundle this operator was built and tested against — both routes above
install the matching `1.23.0` release. Note the Percona operator's own
native monitoring path (PMM) isn't what `MongoDBCluster.spec.monitoring`
drives — see Scope above.

### Strimzi Kafka Operator (for `KafkaCluster`)

```sh
helm install strimzi-kafka-operator \
  oci://quay.io/strimzi-helm/strimzi-kafka-operator \
  --version 1.2.0 -n strimzi-operator-system --create-namespace
```

Verify:

```sh
kubectl get pods -n strimzi-operator-system
kubectl get crd kafkas.kafka.strimzi.io kafkanodepools.kafka.strimzi.io
```

Manifest fallback (no Helm):

```sh
kubectl apply --server-side -f \
  https://github.com/strimzi/strimzi-kafka-operator/releases/download/1.2.0/strimzi-cluster-operator-1.2.0.yaml \
  -n strimzi-operator-system
```

`external-crds/crd-strimzi-kafka-operator-v1.2.0.yaml` is the CRD bundle
this operator was built and tested against — both routes above install the
matching `1.2.0` release. Strimzi is KRaft-only as of this version (no
ZooKeeper to install separately).

### Loki Operator (for `LokiInstance`)

Unlike every dependency above, the Loki Operator ships no Helm chart, and
its kustomize layout doesn't follow the standard kubebuilder `config/default`
convention every other operator here uses — it's split into
`config/overlays/{community,community-openshift,development,openshift}`
instead. `community` is the one for a plain (non-OpenShift) cluster, and it
already points at the real, current release image. The overlay sets
`namespace: loki-operator` on every resource but doesn't include a
`Namespace` manifest of its own, so create it first or the apply fails with
`namespaces "loki-operator" not found`:

```sh
kubectl create namespace loki-operator
kubectl apply -k "https://github.com/grafana/loki/operator/config/overlays/community?ref=operator/v0.11.0"
```

This needs [cert-manager](https://cert-manager.io) installed **first**: the
overlay wires up a `ValidatingWebhookConfiguration` (covering `LokiStack`
and the Loki Operator's other CRDs) backed by a cert-manager
`Issuer`/`Certificate` for its TLS, and that webhook's `failurePolicy` is
`Fail` — without cert-manager running, the `Certificate`/`Issuer` objects
can't even be created (their CRDs won't exist), the webhook never gets a
valid serving cert, and every `LokiStack` create/update is then rejected by
the API server outright. None of this project's other dependencies need
cert-manager; this one does.

```sh
# if cert-manager isn't already installed:
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
```

If you'd rather avoid the kustomize/cert-manager route entirely, the
operator is also published to
[OperatorHub](https://operatorhub.io/operator/loki-operator) (package
`loki-operator`) for installation via
[Operator Lifecycle Manager](https://olm.operatorframework.io) (OLM), which
manages the webhook certificates itself instead of relying on cert-manager.

Verify:

```sh
kubectl get pods -n loki-operator
kubectl get crd lokistacks.loki.grafana.com
```

`external-crds/crd-loki-operator-v0.11.0.yaml` is the CRD this operator was
built and tested against, matching the `operator/v0.11.0` tag and
`docker.io/grafana/loki-operator:0.11.0` image used above — if you install a
different version, double-check `internal/loki`'s field paths still match.

You'll also need an S3-compatible bucket and a Secret with its credentials
before creating a `LokiInstance` — see `LokiInstanceSpec.ObjectStorage`'s
doc comment in `api/v1alpha1/lokiinstance_types.go` for the exact key
layout (`bucketnames`, `endpoint`, `region`, `access_key_id`,
`access_key_secret`), e.g.:

```sh
kubectl create secret generic example-loki-s3 \
  --from-literal=bucketnames=my-loki-bucket \
  --from-literal=endpoint=https://s3.us-east-1.amazonaws.com \
  --from-literal=region=us-east-1 \
  --from-literal=access_key_id=<id> \
  --from-literal=access_key_secret=<secret>
```

### Once the dependency operators are up

Install paas-operator's own CRDs and controller (see below), then the
samples in `config/samples/` — `paas_v1alpha1_postgrescluster.yaml`,
`paas_v1alpha1_valkeycluster.yaml`, `paas_v1alpha1_grafanainstance.yaml`,
`paas_v1alpha1_mariadbcluster.yaml`, `paas_v1alpha1_rabbitmqcluster.yaml`,
`paas_v1alpha1_prometheusinstance.yaml`, `paas_v1alpha1_mongodbcluster.yaml`,
`paas_v1alpha1_kafkacluster.yaml`, `paas_v1alpha1_lokiinstance.yaml` (after
creating the object storage Secret it references, see above) — double as a
smoke test that each dependency operator is reachable and correctly
versioned.

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.
- The dependency operators installed — see "Installing the Dependency
  Operators" above — for whichever CRs (`PostgresCluster`, `ValkeyCluster`,
  `GrafanaInstance`, `MariaDBCluster`, `RabbitMQCluster`,
  `PrometheusInstance`, `MongoDBCluster`, `KafkaCluster`, `LokiInstance`)
  you plan to create.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/paas-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/paas-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/paas-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/paas-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

