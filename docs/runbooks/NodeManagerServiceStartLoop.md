# NodeManagerServiceStartLoop

**Severity:** warning

## Meaning

nodemanager keeps (re)starting a service on a node: the service either exits
shortly after start (`result="exited"`) or has been started more than ~0.5/min
for 15 minutes. The `service` and `node` labels identify the flapping unit.

## Impact

The service is not staying up, and each restart churns the node (logs, possible
dependent-service flaps). Whatever the service provides is effectively
unavailable or intermittent.

## Diagnosis

This is almost always a broken config file the service refuses to load. Inspect
the service's own output and the controller's restart reasoning:

```sh
kubectl logs -n nodemanager -l app=nodemanager --since=20m | \
  grep -i -E 'service exited shortly after|restart|start'
```

On the node:

```sh
# systemd: systemctl status <service>; journalctl -u <service> -n 80
# FreeBSD: service <service> status; tail the service's logfile
```

## Remediation

- **Fix the config**: validate the rendered config the service consumes; a
  ConfigSet file validator (`validate` command) can gate writes so a bad render
  never reaches the service.
- **Check the template**: if the file also shows perpetual drift, resolve that
  first — see `NodeManagerFileDrift`.
- **Dependency missing**: confirm any binary/socket/port the service needs is
  present before it starts.
