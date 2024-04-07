{
  local this = self,

  kind: 'pipeline',
  // type: 'kubernetes',
  name: 'ci',
  steps: [
    {
      name: 'build',
      image: 'zachfi/build-image',
      pull: 'always',
      commands: [
        'make build',
      ],
    },
    {
      name: 'test',
      image: 'zachfi/build-image',
      pull: 'always',
      commands: [
        'make test',
      ],
    },
    {
      name: 'snapshot',
      image: 'zachfi/build-image',
      pull: 'always',
      commands: [
        'make snapshot',
      ],
    },
    {
      name: 'release',
      image: 'zachfi/build-image',
      pull: 'always',
      commands: [
        'make release',
      ],
      environment: {
        GITHUB_TOKEN: {
          from_secret: 'GITHUB_TOKEN',
        },
      },
      when: {
        ref: [
          'refs/tags/v*',
        ],
      },
    },
  ],
  trigger: {
    ref: [
      'refs/heads/*',
      'refs/tags/*',
    ],
  },
}
