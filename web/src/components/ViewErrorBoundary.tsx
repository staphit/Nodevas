import { Component, type ErrorInfo, type ReactNode } from "react";
import { useI18n } from "../i18n";

/**
 * A view that throws, or whose lazy chunk fails to load, would otherwise
 * unmount the whole app and leave a blank page. This keeps the rest of the
 * app on screen and says what happened instead.
 *
 * A chunk goes missing when the server was updated after the page loaded
 * (the old file names are gone) or when the server is unreachable. React
 * keeps the failed import, so only a reload brings that view back; any other
 * error can be retried in place. A new resetKey (another view) clears it.
 */
export function isChunkLoadError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /dynamically imported module|Importing a module script failed|Unable to preload CSS/i.test(message);
}

interface Props {
  children: ReactNode;
  resetKey?: unknown;
  /**
   * For a floating panel or dialog: a small notice with Reload, which can be
   * dismissed until the resetKey changes, instead of a page-sized panel where
   * the floating thing used to be.
   */
  compact?: boolean;
  /**
   * The root boundary's way out of a crash tied to what the app opens on:
   * shown as "Reset view", it is expected to forget that and reload.
   */
  onResetView?: () => void;
}

interface State {
  error: unknown;
  dismissed: boolean;
}

export class ViewErrorBoundary extends Component<Props, State> {
  state: State = { error: null, dismissed: false };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return { error: error ?? new Error("unknown error"), dismissed: false };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("[nodevas] view crashed", error, info.componentStack);
  }

  componentDidUpdate(previous: Props) {
    if (this.state.error && previous.resetKey !== this.props.resetKey) {
      this.setState({ error: null, dismissed: false });
    }
  }

  render() {
    const { error, dismissed } = this.state;
    if (!error) return this.props.children;
    if (this.props.compact) {
      return dismissed ? null : (
        <CompactViewError error={error} onDismiss={() => this.setState({ dismissed: true })} />
      );
    }
    return (
      <ViewError
        error={error}
        onRetry={() => this.setState({ error: null })}
        onResetView={this.props.onResetView}
      />
    );
  }
}

function ViewError({
  error,
  onRetry,
  onResetView,
}: {
  error: unknown;
  onRetry: () => void;
  onResetView?: () => void;
}) {
  const { t } = useI18n();
  const chunk = isChunkLoadError(error);
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div className="view-error" role="alert">
      <h2>{t(chunk ? "viewError.chunkTitle" : "viewError.title")}</h2>
      <p>{t(chunk ? "viewError.chunkBody" : "viewError.body")}</p>
      <div className="view-error-actions">
        <button type="button" className="view-error-primary" onClick={() => window.location.reload()}>
          {t("viewError.reload")}
        </button>
        {!chunk && (
          <button type="button" onClick={onRetry}>
            {t("viewError.retry")}
          </button>
        )}
        {onResetView && (
          <button type="button" onClick={onResetView} title={t("viewError.resetViewHint")}>
            {t("viewError.resetView")}
          </button>
        )}
      </div>
      <details>
        <summary>{t("viewError.details")}</summary>
        <pre>{message}</pre>
      </details>
    </div>
  );
}

function CompactViewError({ error, onDismiss }: { error: unknown; onDismiss: () => void }) {
  const { t } = useI18n();
  const chunk = isChunkLoadError(error);
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div className="view-error-compact" role="alert" title={message}>
      <span>{t(chunk ? "viewError.compactChunk" : "viewError.compact")}</span>
      <button type="button" className="view-error-primary" onClick={() => window.location.reload()}>
        {t("viewError.reload")}
      </button>
      <button type="button" className="view-error-dismiss" onClick={onDismiss} aria-label={t("viewError.dismiss")}>
        ×
      </button>
    </div>
  );
}
