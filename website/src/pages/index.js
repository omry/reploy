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
          <p className={styles.heroSubtitle}>
            Cross-platform app installs from portable blueprints.
          </p>
          <p className={styles.heroAvailability}>
            Reploy turns an app blueprint into a working install with staging,
            health checks, logs, control commands, updates, and uninstall.
          </p>
          <div className={styles.heroActions}>
            <Link className="button button--primary button--lg" to="/docs/install-an-app">
              Install an app
            </Link>
            <Link className="button button--secondary button--lg" to="/docs/author-deployments">
              Create blueprint
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
reploy install --scope user --to <install-dir>
<install-dir>/<app>ctl status`}</code>
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
      description="Reploy cross-platform app installer documentation">
      <HomepageHeader />
      <main className={styles.main}>
        <section className={styles.contract}>
          <div>
            <h2>Blueprints describe app intent.</h2>
            <p>
              App authors declare packages, config paths, data mounts, ports,
              health checks, install defaults, and app commands once. Reploy
              turns that semantic contract into a staged or installed app on the
              current host.
            </p>
          </div>
          <div>
            <h2>Installers manage the app lifecycle.</h2>
            <p>
              Reploy handles staging, bundle preparation, runtime warmup,
              install, update, control scripts, logs, status, health checks, and
              uninstall. Host package managers can install Reploy itself; they
              do not replace the app blueprint contract.
            </p>
          </div>
        </section>
      </main>
    </Layout>
  );
}
