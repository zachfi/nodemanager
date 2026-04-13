// webhook.libsonnet — deployment resources for the nodemanager validating
// admission webhook.
//
// Usage:
//   local webhook = import 'webhook.libsonnet';
//   webhook.new('nodemanager', 'ghcr.io/zachfi/nodemanager-webhook:v0.9.0')
//
// Requires cert-manager for TLS certificate provisioning. The webhook
// expects a Certificate named nodemanager-webhook-cert in the same namespace.

{
  new(namespace, image):: {
    local this = self,

    serviceAccount: {
      apiVersion: 'v1',
      kind: 'ServiceAccount',
      metadata: {
        name: 'nodemanager-webhook',
        namespace: namespace,
      },
    },

    deployment: {
      apiVersion: 'apps/v1',
      kind: 'Deployment',
      metadata: {
        name: 'nodemanager-webhook',
        namespace: namespace,
        labels: {
          app: 'nodemanager-webhook',
        },
      },
      spec: {
        replicas: 1,
        selector: {
          matchLabels: {
            app: 'nodemanager-webhook',
          },
        },
        template: {
          metadata: {
            labels: {
              app: 'nodemanager-webhook',
            },
          },
          spec: {
            serviceAccountName: 'nodemanager-webhook',
            containers: [{
              name: 'webhook',
              image: image,
              args: [
                '--cert-dir=/certs',
                '--port=9443',
              ],
              ports: [{
                containerPort: 9443,
                name: 'webhook',
                protocol: 'TCP',
              }],
              livenessProbe: {
                httpGet: {
                  path: '/healthz',
                  port: 8081,
                },
              },
              readinessProbe: {
                httpGet: {
                  path: '/readyz',
                  port: 8081,
                },
              },
              resources: {
                requests: {
                  cpu: '10m',
                  memory: '32Mi',
                },
                limits: {
                  memory: '64Mi',
                },
              },
              volumeMounts: [{
                name: 'certs',
                mountPath: '/certs',
                readOnly: true,
              }],
            }],
            volumes: [{
              name: 'certs',
              secret: {
                secretName: 'nodemanager-webhook-cert',
              },
            }],
          },
        },
      },
    },

    service: {
      apiVersion: 'v1',
      kind: 'Service',
      metadata: {
        name: 'nodemanager-webhook',
        namespace: namespace,
      },
      spec: {
        selector: {
          app: 'nodemanager-webhook',
        },
        ports: [{
          port: 443,
          targetPort: 9443,
          protocol: 'TCP',
        }],
      },
    },

    webhookConfiguration: {
      apiVersion: 'admissionregistration.k8s.io/v1',
      kind: 'ValidatingWebhookConfiguration',
      metadata: {
        name: 'nodemanager-webhook',
        annotations: {
          'cert-manager.io/inject-ca-from': namespace + '/nodemanager-webhook-cert',
        },
      },
      webhooks: [{
        name: 'validate.nodemanager.nodemanager',
        admissionReviewVersions: ['v1'],
        sideEffects: 'None',
        failurePolicy: 'Fail',
        clientConfig: {
          service: {
            name: 'nodemanager-webhook',
            namespace: namespace,
            path: '/validate-nodemanager',
          },
        },
        rules: [
          {
            apiGroups: ['common.nodemanager'],
            apiVersions: ['v1'],
            resources: ['managednodes'],
            operations: ['UPDATE'],
          },
          {
            apiGroups: ['freebsd.nodemanager'],
            apiVersions: ['v1'],
            resources: ['jails'],
            operations: ['UPDATE'],
          },
        ],
      }],
    },
  },
}
