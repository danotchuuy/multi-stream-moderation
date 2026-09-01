import { useState } from "react";
import { fetchChannelInfo, searchCategories, updateChannelInfo } from "../lib/api";
import { StreamInfoEditor, type CategoryOption } from "./StreamInfoEditor";

type Props = {
  channel: string;
};

/** A collapsible "Edit stream info" menu shown at the top of the Twitch
 * panel, only for the channel's own broadcaster — Twitch's Modify
 * Channel Information API requires broadcaster_id to match the caller's
 * own user ID, so a moderator can never use this even for a channel they
 * otherwise moderate. */
export function TwitchChannelInfoMenu({ channel }: Props) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState<CategoryOption<string> | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const openMenu = () => {
    setOpen(true);
    setLoading(true);
    setLoadError(null);
    fetchChannelInfo(channel)
      .then((info) => {
        setTitle(info.title);
        setCategory(
          info.categoryId
            ? { id: info.categoryId, name: info.categoryName, image: "" }
            : null,
        );
      })
      .catch((err) =>
        setLoadError(err instanceof Error ? err.message : "failed to load channel info"),
      )
      .finally(() => setLoading(false));
  };

  if (!open) {
    return (
      <button type="button" className="link-button channel-info-toggle" onClick={openMenu}>
        Edit stream info
      </button>
    );
  }

  return (
    <div className="channel-info-panel">
      {loading && <p className="hint">Loading...</p>}
      {loadError && <p className="error">{loadError}</p>}
      {!loading && !loadError && (
        <StreamInfoEditor
          title={title}
          category={category}
          onSearch={searchCategories}
          onSave={(updates) => updateChannelInfo(channel, updates)}
          onSaved={() => setOpen(false)}
          onCancel={() => setOpen(false)}
        />
      )}
    </div>
  );
}
