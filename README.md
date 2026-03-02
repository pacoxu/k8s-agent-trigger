# k8s-agent-trigger

一个轻量的 Kubernetes-native 事件触发与运行骨架，专门为 agent-driven 验收、诊断、建议而设计。

A lightweight Kubernetes-native event trigger skeleton designed for agent-driven acceptance testing, diagnostics, and recommendations.

[![CI](https://github.com/pacoxu/k8s-agent-trigger/actions/workflows/ci.yml/badge.svg)](https://github.com/pacoxu/k8s-agent-trigger/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

## Overview

`k8s-agent-trigger` fills the gap between Kubernetes-native controllers and intelligent Agent systems. It watches cluster resources and automatically dispatches trigger events to your Agent service when notable events occur — without polling, without manual intervention.

**Key features:**
- 🔍 **Event-driven**: Watches Deployments, Jobs, and Pods using Kubernetes native controller-runtime
- 🎯 **Predicate filtering**: Only triggers on meaningful changes (generation bumps, failures, crash loops)
- 🚀 **Dispatch**: Sends structured JSON payloads to your Agent HTTP endpoint
- 📝 **Run recording**: Persists Agent execution results in a Kubernetes ConfigMap
- 🔒 **Minimal RBAC**: Only requests `get/list/watch` permissions on watched resources
- ♻️ **Leader election**: Safe to run with multiple replicas

## Trigger Scenarios

| Trigger | Watch | Predicate | Dispatch |
|---------|-------|-----------|----------|
| Deployment update | `apps/v1.Deployment` | `generation` changed | `triggerType: DeploymentUpdate` |
| Job failure | `batch/v1.Job` | `status.conditions[Failed]=True` | `triggerType: JobFailed` |
| Pod CrashLoop | `v1.Pod` | `restartCount >= 3 && !Ready` | `triggerType: PodCrashLoop` |

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.25+)
- An Agent HTTP service that accepts `POST` requests with a JSON payload

### Deploy

```bash
# 1. Create the namespace
kubectl create namespace k8s-agent-trigger-system

# 2. Apply RBAC
kubectl apply -f config/rbac/role.yaml

# 3. Create the secret with your Agent endpoint
kubectl create secret generic k8s-agent-trigger-config \
  --namespace=k8s-agent-trigger-system \
  --from-literal=agent-endpoint=http://your-agent-service/api/v1/agent/run

# 4. Deploy the controller
kubectl apply -f config/manager/manager.yaml
```

### Running Locally

```bash
go run ./cmd/ \
  --agent-endpoint=http://localhost:9090/api/v1/agent/run \
  --recorder-namespace=default \
  --leader-elect=false
```

## Agent Interface

### Trigger Request (POST)

```json
{
  "triggerType": "DeploymentUpdate",
  "namespace": "prod",
  "name": "web-app",
  "generation": 3,
  "timestamp": "2026-03-02T11:35:00Z"
}
```

### Expected Agent Response

```json
{
  "status": "passed",
  "summary": "Deployment web-app v3 rolled out successfully. No issues detected.",
  "actions": []
}
```

### Run Record (stored in ConfigMap `agent-run-history`)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: agent-run-history
  namespace: default
data:
  "prod/web-app_v3": |
    {"status":"passed","summary":"Deployment web-app v3 rolled out successfully.","timestamp":"2026-03-02T11:40:00Z"}
```

## Architecture

```
K8s API Server
     │
     ▼ (Watch/List)
┌─────────────────────────────────────┐
│         k8s-agent-trigger           │
│                                     │
│  Deployment ──► Predicate ──► Map   │
│  Job        ──► Predicate ──► Map   │──► Dispatcher ──► Agent Service
│  Pod        ──► Predicate ──► Map   │         │
│                                     │         ▼
│                                     │    Run Recorder
│                                     │    (ConfigMap)
└─────────────────────────────────────┘
```

## Project Structure

```
k8s-agent-trigger/
├── cmd/                    # Main entry point
│   └── main.go
├── controllers/            # Kubernetes reconcilers
│   ├── deployment_controller.go
│   ├── job_controller.go
│   └── pod_controller.go
├── pkg/
│   ├── dispatcher/         # HTTP dispatcher
│   │   └── http.go
│   └── recorder/           # ConfigMap run recorder
│       └── configmap.go
├── config/
│   ├── rbac/               # RBAC manifests
│   ├── manager/            # Controller deployment manifest
│   └── samples/            # Example trigger resources
├── .github/workflows/      # GitHub Actions CI
├── Dockerfile
└── README.md
```

## Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--agent-endpoint` | _(required)_ | HTTP endpoint of the Agent service |
| `--recorder-namespace` | `default` | Namespace for the `agent-run-history` ConfigMap |
| `--agent-timeout` | `30s` | HTTP request timeout for Agent calls |
| `--leader-elect` | `false` | Enable leader election for HA deployments |
| `--metrics-bind-address` | `:8080` | Address for Prometheus metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Address for health/readiness probes |

## Development Roadmap

| Phase | Goals | Timeline |
|-------|-------|----------|
| Phase 1 (MVP) | Basic controller, Deployment trigger, HTTP dispatch, ConfigMap recording | 1–2 months |
| Phase 2 | Job/Pod triggers, CRD-based AgentRun records, rate limiting, leader election | 2–3 months |
| Phase 3 | OPA/Kyverno policy integration, observability (Prometheus/OTel), multi-tenant support | 2–3 months |

## License

Apache-2.0 — see [LICENSE](LICENSE).
