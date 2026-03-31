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

[
  (
    pipeline('ci') {
      steps:
        [
          make('build'),
          make('test'),
        ],
    }
  ),
  (
    pipeline('main')
    + withPipelineMain() {
      steps:
        [
          make('snapshot'),
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
