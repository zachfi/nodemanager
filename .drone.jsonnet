local owner = 'zachfi';
local localRegistry = 'reg.dist.svc.cluster.znet:5000';

local pipeline(name) = {
  kind: 'pipeline',
  name: name,
  steps: [],
  depends_on: [],
  trigger: {
    ref: [
      'refs/heads/main',
      'refs/heads/dependabot/**',
      'refs/tags/v*',
    ],
  },
};

local withPipelineMain() = {
  trigger: {
    ref: [
      'refs/heads/main',
    ],
  },
};

local withPipelineTags() = {
  trigger: {
    ref: [
      'refs/tags/v*',
    ],
  },
};

local step(name) = {
  name: name,
  image: '%s/%s/tools' % [localRegistry, owner],
  pull: 'always',
  commands: [],
};

local make(target) = step(target) {
  commands: ['make %s' % target],
};

local withHeads() = {
  when+: {
    ref+: [
      'refs/heads/*',
    ],
  },
};

local withTags() = {
  when+: {
    ref+: [
      'refs/tags/v*',
    ],
  },
};

local withGithub() = {
  environment+: {
    GITHUB_TOKEN: {
      from_secret: 'GITHUB_TOKEN',
    },
  },
};

local withCI() = {
  environment+: {
    CI: 'true',
  },
};

local dockerCIStep() = step('docker-ci') {
  // Build and push a versioned image to the local registry.
  // Requires the host to have docker socket access (volumes wired in pipeline).
  commands: ['make docker-ci REGISTRY=%s/%s' % [localRegistry, owner]],
  volumes: [{ name: 'docker-sock', path: '/var/run/docker.sock' }],
};

local withDockerSock() = {
  volumes+: [{ name: 'docker-sock', host: { path: '/var/run/docker.sock' } }],
};

local testStep() = step('test') {
  // Point directly at the envtest binaries pre-installed in the tools image by
  //   setup-envtest use 1.29.0 --bin-dir /usr/local/kubebuilder/bin
  // The Makefile uses ${KUBEBUILDER_ASSETS:-$(setup-envtest ...)} so this value
  // takes priority; setup-envtest is not invoked and no network access is needed.
  commands: ['make test'],
  environment+: {
    KUBEBUILDER_ASSETS: '/usr/local/kubebuilder/bin/k8s/1.29.0-linux-amd64',
  },
};

[
  (
    pipeline('ci') {
      steps:
        [
          make('build'),
          testStep(),
        ],
    }
  ),
  (
    pipeline('main')
    + withPipelineMain()
    + withDockerSock() {
      steps:
        [
          make('snapshot'),
          dockerCIStep(),
        ],
    }
  ),
  (
    pipeline('release')
    + withPipelineTags() {
      steps:
        [
          make('release')
          + withGithub()
          + withTags(),
        ],
    }
  ),
  (
    pipeline('downstream')
    + withPipelineTags()
    + { depends_on: ['release'] } {
      steps:
        [
          make('release-downstream')
          + withGithub()
          + withCI()
          + withTags(),
        ],
    }
  ),
]
