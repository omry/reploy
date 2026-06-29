import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import styles from './index.module.css';

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
          <p className={styles.heroAvailability}>
            Reploy is currently available on Linux only. Other operating
            systems may be added later.
          </p>
          <div className={styles.heroActions}>
            <Link className="button button--primary button--lg" to="/docs/">
              Read the docs
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
reploy stage <app-blueprint-ref>
reploy up
reploy test`}</code>
          </pre>
        </div>
      </div>
    </header>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="Reploy deployment lifecycle documentation">
      <HomepageHeader />
    </Layout>
  );
}
