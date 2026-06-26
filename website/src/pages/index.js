import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import styles from './index.module.css';

const cards = [
  {
    title: 'App User',
    body: 'Use a blueprint ref from the app author to stage, test, install, and manage an app.',
    to: '/docs/install-an-app',
  },
  {
    title: 'App Author',
    body: 'Publish app defaults, bundle options, runtime commands, health checks, and install hooks in a blueprint.',
    to: '/docs/author-deployments',
  },
  {
    title: 'What is supported?',
    body: 'See backend, runtime, and host operating-system support as separate dimensions.',
    to: '/docs/support-matrix',
  },
];

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={styles.hero}>
      <div className={styles.heroInner}>
        <div className={styles.heroCopy}>
          <Heading as="h1" className={styles.heroTitle}>
            {siteConfig.title}
          </Heading>
          <p className={styles.heroSubtitle}>{siteConfig.tagline}</p>
          <div className={styles.heroActions}>
            <Link className="button button--primary button--lg" to="/docs/install-an-app">
              App User
            </Link>
            <Link className="button button--secondary button--lg" to="/docs/author-deployments">
              App Author
            </Link>
          </div>
        </div>
        <div className={styles.terminal} aria-label="Reploy install command">
          <div className={styles.terminalBar}>
            <span />
            <span />
            <span />
          </div>
          <pre>
            <code>{`curl -fsSL https://reploy.yadan.net/install.sh | sh
reploy init --blueprint <app-blueprint-ref>
reploy bundle build
reploy up
reploy test`}</code>
          </pre>
        </div>
      </div>
    </header>
  );
}

function FeatureCards() {
  return (
    <section className={styles.cards}>
      <div className="container">
        <div className={styles.cardGrid}>
          {cards.map((card) => (
            <Link className={clsx(styles.card)} to={card.to} key={card.title}>
              <Heading as="h2">{card.title}</Heading>
              <p>{card.body}</p>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="Reploy deployment lifecycle documentation">
      <HomepageHeader />
      <main>
        <FeatureCards />
      </main>
    </Layout>
  );
}
