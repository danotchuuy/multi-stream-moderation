import { youtubeLoginUrl } from "../lib/youtubeApi";

type Props = {
  error?: string | null;
};

export function ConnectYouTube({ error }: Props) {
  return (
    <div className="card youtube-panel">
      <h2>Connect your YouTube account</h2>
      <p>
        Authorize with Google to watch and moderate a live stream's chat.
      </p>
      {error && <p className="error">{error}</p>}
      <a className="button youtube-button" href={youtubeLoginUrl()}>
        Connect YouTube account
      </a>
      <p className="youtube-privacy-note">
        By connecting, you agree to the{" "}
        <a href="/privacy.html" target="_blank" rel="noopener noreferrer">
          Privacy Policy
        </a>
        .
      </p>
    </div>
  );
}
