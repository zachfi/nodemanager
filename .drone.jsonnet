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
  ],
}
