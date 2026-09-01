import { badgeTitle, badgeVisual, type ChatBadge } from "../lib/badges";

type Props = {
  badges: ChatBadge[];
};

/** Renders a chatter's badges (broadcaster, moderator, VIP, subscriber,
 * ...) to the left of their name. Shared between the Twitch and Kick
 * chat panels — both send the same badge shape. */
export function ChatBadges({ badges }: Props) {
  if (badges.length === 0) {
    return null;
  }

  return (
    <span className="chat-badges">
      {badges.map((badge, i) => (
        <span
          key={`${badge.name}-${i}`}
          className="chat-badge"
          title={badgeTitle(badge)}
        >
          {badgeVisual(badge).icon}
        </span>
      ))}
    </span>
  );
}
