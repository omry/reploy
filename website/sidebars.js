// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    'intro',
    {
      type: 'category',
      label: 'Install',
      items: ['install-script', 'install-pypi'],
    },
    {
      type: 'category',
      label: 'App User',
      link: {
        type: 'doc',
        id: 'install-an-app',
      },
      items: ['blueprints', 'uninstall'],
    },
    {
      type: 'category',
      label: 'App Author',
      link: {
        type: 'doc',
        id: 'author-deployments',
      },
      items: ['blueprint-structure', 'bundles'],
    },
    {
      type: 'category',
      label: 'Project',
      items: ['support-matrix', 'release'],
    },
  ],
};

module.exports = sidebars;
