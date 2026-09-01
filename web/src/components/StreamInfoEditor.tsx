import { useEffect, useState, type FormEvent } from "react";

// Kick's category search rejects anything shorter than 3 characters;
// Twitch has no such limit but there's no reason to search more eagerly
// than Kick allows, so both platforms share this threshold.
const MIN_QUERY_LENGTH = 3;
const SEARCH_DEBOUNCE_MS = 300;

export type CategoryOption<Id> = {
  id: Id;
  name: string;
  image: string;
};

type Props<Id> = {
  title: string;
  category: CategoryOption<Id> | null;
  onSearch: (query: string) => Promise<CategoryOption<Id>[]>;
  onSave: (updates: { title?: string; categoryId?: Id }) => Promise<void>;
  onSaved: () => void;
  onCancel: () => void;
};

/** A title + category form shared by the Twitch and Kick stream-info
 * menus — the two platforms differ only in category ID type (string vs.
 * number) and in how search/save actually hit the network, both passed
 * in as props. */
export function StreamInfoEditor<Id>({
  title: initialTitle,
  category: initialCategory,
  onSearch,
  onSave,
  onSaved,
  onCancel,
}: Props<Id>) {
  const [title, setTitle] = useState(initialTitle);
  const [categoryQuery, setCategoryQuery] = useState(initialCategory?.name ?? "");
  const [selectedCategory, setSelectedCategory] = useState<CategoryOption<Id> | null>(
    initialCategory,
  );
  const [categoryDirty, setCategoryDirty] = useState(false);
  const [options, setOptions] = useState<CategoryOption<Id>[]>([]);
  const [searching, setSearching] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const query = categoryQuery.trim();
    if (query.length < MIN_QUERY_LENGTH) {
      setOptions([]);
      return;
    }

    let cancelled = false;
    setSearching(true);
    const timer = setTimeout(() => {
      onSearch(query)
        .then((results) => {
          if (!cancelled) setOptions(results);
        })
        .catch(() => {
          if (!cancelled) setOptions([]);
        })
        .finally(() => {
          if (!cancelled) setSearching(false);
        });
    }, SEARCH_DEBOUNCE_MS);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [categoryQuery]);

  const titleDirty = title.trim() !== "" && title.trim() !== initialTitle;
  const canSave = (titleDirty || categoryDirty) && !saving;

  const handlePick = (opt: CategoryOption<Id>) => {
    setSelectedCategory(opt);
    setCategoryQuery(opt.name);
    setCategoryDirty(opt.id !== initialCategory?.id);
    setOptions([]);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!canSave) return;

    setSaving(true);
    setError(null);
    try {
      const updates: { title?: string; categoryId?: Id } = {};
      if (titleDirty) updates.title = title.trim();
      if (categoryDirty && selectedCategory) updates.categoryId = selectedCategory.id;
      await onSave(updates);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed to update stream info");
    } finally {
      setSaving(false);
    }
  };

  return (
    <form className="channel-info-form" onSubmit={submit}>
      {error && <p className="error">{error}</p>}

      <label className="channel-info-field">
        Title
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={140}
        />
      </label>

      <label className="channel-info-field category-field">
        Category
        <input
          type="text"
          value={categoryQuery}
          onChange={(e) => {
            setCategoryQuery(e.target.value);
            setCategoryDirty(false);
          }}
          placeholder="Search for a category..."
          autoComplete="off"
        />
        {searching && <span className="hint">Searching...</span>}
        {options.length > 0 && (
          <ul className="category-options">
            {options.map((opt) => (
              <li key={String(opt.id)}>
                <button type="button" onClick={() => handlePick(opt)}>
                  {opt.image && <img src={opt.image} alt="" />}
                  {opt.name}
                </button>
              </li>
            ))}
          </ul>
        )}
      </label>

      <div className="channel-info-actions">
        <button type="submit" className="button" disabled={!canSave}>
          {saving ? "Saving..." : "Save"}
        </button>
        <button type="button" className="link-button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
