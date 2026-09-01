import { twitchLoginUrl } from "../lib/api";

type Props = {
  error?: string | null;
};

export function ConnectTwitch({ error }: Props) {
  return (
    <div className="card">
      <h2>Connect your Twitch account</h2>
      <p>
        Authorize with Twitch to see the channels you moderate and pick one
        to watch chat from.
      </p>
      {error && <p className="error">{error}</p>}
      <a className="button" href={twitchLoginUrl()}>
        Connect Twitch account
      </a>
    </div>
  );
}
