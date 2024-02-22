{
  local this = self,

  kind: 'pipeline',
  // type: 'kubernetes',
  name: 'build',
  steps: [
    {
      name: 'make',
      image: 'golang',
      commands: [
        'make',
      ],
    },
  ],
}
