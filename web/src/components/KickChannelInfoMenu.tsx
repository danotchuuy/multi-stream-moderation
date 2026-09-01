import { useState } from "react";
import {
  fetchKickChannelInfo,
  searchKickCategories,
  updateKickChannelInfo,
} from "../lib/kickApi";
import { StreamInfoEditor, type CategoryOption } from "./StreamInfoEditor";

type Props = {
  channel: string;
};

/** A collapsible "Edit stream info" menu shown at the top of the Kick
 * panel, only for the channel's own broadcaster — Kick's update endpoint
 * has no broadcaster ID to pass, so it can only ever act on the
 * signed-in user's own channel. */
export function KickChannelInfoMenu({ channel }: Props) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState<CategoryOption<number> | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const openMenu = () => {
    setOpen(true);
    setLoading(true);
    setLoadError(null);
    fetchKickChannelInfo(channel)
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
          onSearch={searchKickCategories}
          onSave={updateKickChannelInfo}
          onSaved={() => setOpen(false)}
          onCancel={() => setOpen(false)}
        />
      )}
    </div>
  );
}
