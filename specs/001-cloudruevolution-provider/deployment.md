# Spec 001 — Kubernetes Deployment Guide

How to use `ghcr.io/metrica-pro/lego` with cert-manager for DNS-01 challenges
against Cloud.ru Evolution DNS.

## Option A — cert-manager + lego webhook (recommended)

Use a cert-manager ACME webhook solver that shells out to lego or calls the DNS API
directly. This keeps TLS automation fully inside the cluster.

> **Not yet implemented.** This is the target architecture for the next iteration.
> Track in Plane.

## Option B — lego CLI as a CronJob / one-shot Job (current approach)

Run lego as a Kubernetes Job. Secrets from Infisical / Kubernetes Secret.
Certificates stored in a PVC or Secret. Manually trigger renewal or schedule via CronJob.

### 1. Kubernetes Secret for Cloud.ru credentials

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloudru-evolution-dns
  namespace: cert-manager
type: Opaque
stringData:
  key-id: "<CLOUDRU_EVOLUTION_KEY_ID>"
  secret: "<CLOUDRU_EVOLUTION_SECRET>"
  project-id: "<CLOUDRU_EVOLUTION_PROJECT_ID>"
```

> In production load via Infisical Operator or External Secrets Operator — never commit
> plaintext credentials to git.

### 2. PersistentVolumeClaim for certificates

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: lego-certs
  namespace: cert-manager
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 100Mi
```

### 3. Job — obtain or renew certificate

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: lego-run
  namespace: cert-manager
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: lego
          image: ghcr.io/metrica-pro/lego:v5.1.0
          args:
            - --email=ops@example.com
            - --dns=cloudruevolution
            - --path=/etc/lego
            - --server=https://acme-v02.api.letsencrypt.org/directory
            - -d
            - "*.example.com"
            - -d
            - example.com
            - run
          env:
            - name: CLOUDRU_EVOLUTION_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: cloudru-evolution-dns
                  key: key-id
            - name: CLOUDRU_EVOLUTION_SECRET
              valueFrom:
                secretKeyRef:
                  name: cloudru-evolution-dns
                  key: secret
            - name: CLOUDRU_EVOLUTION_PROJECT_ID
              valueFrom:
                secretKeyRef:
                  name: cloudru-evolution-dns
                  key: project-id
          volumeMounts:
            - name: certs
              mountPath: /etc/lego
      volumes:
        - name: certs
          persistentVolumeClaim:
            claimName: lego-certs
```

### 4. CronJob — automatic renewal (every 60 days)

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: lego-renew
  namespace: cert-manager
spec:
  schedule: "0 3 1 */2 *"   # 03:00 on the 1st of every other month
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: lego
              image: ghcr.io/metrica-pro/lego:v5.1.0
              args:
                - --email=ops@example.com
                - --dns=cloudruevolution
                - --path=/etc/lego
                - --server=https://acme-v02.api.letsencrypt.org/directory
                - -d
                - "*.example.com"
                - -d
                - example.com
                - renew
                - --days=30
              env:
                - name: CLOUDRU_EVOLUTION_KEY_ID
                  valueFrom:
                    secretKeyRef:
                      name: cloudru-evolution-dns
                      key: key-id
                - name: CLOUDRU_EVOLUTION_SECRET
                  valueFrom:
                    secretKeyRef:
                      name: cloudru-evolution-dns
                      key: secret
                - name: CLOUDRU_EVOLUTION_PROJECT_ID
                  valueFrom:
                    secretKeyRef:
                      name: cloudru-evolution-dns
                      key: project-id
              volumeMounts:
                - name: certs
                  mountPath: /etc/lego
          volumes:
            - name: certs
              persistentVolumeClaim:
                claimName: lego-certs
```

### 5. Using the certificate in another workload

After lego writes the certificate, mount the PVC or copy files to a TLS Secret:

```sh
# One-liner to create/update the TLS Secret from lego output
kubectl create secret tls example-com-tls \
  --cert=/etc/lego/certificates/_.example.com.crt \
  --key=/etc/lego/certificates/_.example.com.key \
  --namespace=default \
  --dry-run=client -o yaml | kubectl apply -f -
```

Or add a post-hook script as a sidecar/init container that runs `kubectl create secret`
after lego exits successfully.

## Flux CD (GitOps)

Add the Job/CronJob manifests under `clusters/<cluster>/cert-manager/` and commit.
Flux will apply them on the next reconcile interval.

For image updates, add an `ImagePolicy` that tracks `ghcr.io/metrica-pro/lego:5.*`
to automatically roll to new patch releases.

## Troubleshooting

**ImagePullBackOff** — check that the cluster has credentials for ghcr.io:

```sh
kubectl create secret docker-registry ghcr-creds \
  --docker-server=ghcr.io \
  --docker-username=<github-user> \
  --docker-password=<ghcr-pat> \
  --namespace=cert-manager
```

Then reference it in the pod spec: `imagePullSecrets: [{name: ghcr-creds}]`.

**DNS challenge timeout** — verify NS propagation:

```sh
dig +short NS <your-domain> @8.8.8.8
# must return evo-cns01.cloud.ru. / evo-cns02.cloud.ru.
```

Increase `CLOUDRU_EVOLUTION_PROPAGATION_TIMEOUT` (default 5m) if needed.
