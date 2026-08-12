import { Link } from 'react-router-dom';
import styles from './NotFoundPage.module.css';

export function NotFoundPage() {
  return (
    <div className={styles.wrap}>
      <h1 className={styles.code}>404</h1>
      <p className={styles.message}>This provider could not be found.</p>
      <Link to="/" className={styles.link}>
        ← Back to catalog
      </Link>
    </div>
  );
}
