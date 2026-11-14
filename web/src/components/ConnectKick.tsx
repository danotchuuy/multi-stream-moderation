import { kickLoginUrl } from "../lib/kickApi";

type Props = {
  error?: string | null;
};

export function ConnectKick({ error }: Props) {
  return (
    <div className="card kick-panel">
      <h2>Connect your Kick account</h2>
      <p>
        Authorize with Kick to watch and moderate a channel's chat. Unlike
        Twitch, Kick delivers chat over a webhook, which needs this server
        reachable from the internet (e.g. via ngrok) — see the README for
        setup.
      </p>
      {error && <p className="error">{error}</p>}
      <a className="button kick-button" href={kickLoginUrl()}>
        Connect Kick account
      </a>
    </div>
  );
}
