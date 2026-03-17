# k8s-agent-trigger Operations Runbook

## Service Objective

- Availability target: controller manager should maintain continuous watch and reconcile execution during normal cluster operation.
- Dispatch objective: trigger dispatch attempts should complete within `agent-timeout` with retries for transient failures.
- Data objective: `agent-run-history` keeps a bounded rolling history in ConfigMap data with deterministic oldest-entry eviction.

## Failure Semantics

- Transient dispatch failures (`429`, `5xx`, network timeouts): retried with exponential backoff and jitter.
- Permanent dispatch failures (`4xx` except `429`, invalid Agent response shape/status): no retry in the same reconcile loop, failure recorded once.
- Duplicate logical events: suppressed by deterministic event key lookup before dispatch.

## Rollback Procedure

1. Roll back controller image tag in `config/manager/manager.yaml` to the previous known-good version.
2. Re-apply manifests:
   - `kubectl apply -f config/rbac/role.yaml`
   - `kubectl apply -f config/manager/manager.yaml`
   - `kubectl apply -f config/manager/pdb.yaml`
   - `kubectl apply -f config/manager/networkpolicy.yaml`
3. Verify rollout:
   - `kubectl -n k8s-agent-trigger-system rollout status deployment/k8s-agent-trigger`
4. Confirm manager logs no repeated dispatch or ConfigMap update errors.

## Emergency Dispatch Disable

- Use `--dispatch-enabled=false` in manager args to stop outbound Agent calls while keeping resource watch/reconcile active.
- Apply the deployment manifest update and verify new pods are running.
- Re-enable by setting `--dispatch-enabled=true` after Agent endpoint recovery.

## Troubleshooting

### Agent endpoint outage

- Symptoms: transient dispatch errors, repeated retries, high dispatch latency.
- Checks:
  - verify Agent service endpoints and DNS resolution from cluster.
  - verify NetworkPolicy egress allows the Agent service port.
  - confirm auth token file/secret is present if token auth is enabled.

### Duplicate trigger suspicion

- Check logs for `"Duplicate ... trigger suppressed"` entries.
- Validate eventID stability and matching record key in `agent-run-history` ConfigMap.
- Inspect if the resource identity changed (new UID means a new logical event source).

### Run history retention issues

- Check `--history-max-entries` value in deployment args.
- Inspect ConfigMap size and oldest/newest timestamps:
  - `kubectl -n k8s-agent-trigger-system get cm agent-run-history -o yaml`
- If history is too small, increase `--history-max-entries` and redeploy.

