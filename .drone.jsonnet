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

local withPipelineTags() = {
  trigger: {
    ref: [
      'refs/tags/v*',
    ],
  },
};

local step(name) = {
  name: name,
  image: 'zachfi/build-image',
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

[
  (
    pipeline('ci') {
      steps:
        [
          make('build'),
          make('test'),
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
]
