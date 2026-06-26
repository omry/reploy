// @ts-check

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Reploy',
  tagline: 'Repeatable app deployments from portable blueprints.',
  favicon: 'img/reploy-mark.svg',

  url: 'https://reploy.yadan.net',
  baseUrl: '/',
  organizationName: 'omry',
  projectName: 'reploy',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: require.resolve('./sidebars.js'),
          routeBasePath: 'docs',
          editUrl: 'https://github.com/omry/reploy/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      image: 'img/reploy-social.png',
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: true,
        respectPrefersColorScheme: false,
      },
      navbar: {
        title: 'Reploy',
        logo: {
          alt: 'Reploy',
          src: 'img/reploy-mark.svg',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Docs',
          },
          {
            href: 'https://github.com/omry/reploy',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {
                label: 'App User',
                to: '/docs/install-an-app',
              },
              {
                label: 'App Author',
                to: '/docs/author-deployments',
              },
              {
                label: 'Support',
                to: '/docs/support-matrix',
              },
            ],
          },
          {
            title: 'Project',
            items: [
              {
                label: 'GitHub',
                href: 'https://github.com/omry/reploy',
              },
              {
                label: 'PyPI',
                href: 'https://pypi.org/project/reploy/',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Omry Yadan.`,
      },
      prism: {
        additionalLanguages: ['bash', 'json', 'yaml'],
      },
    }),
};

module.exports = config;
