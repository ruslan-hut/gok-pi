interface ConfigActionsProps {
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
  onReset: () => void;
}

export function ConfigActions({
  dirty,
  saving,
  onSave,
  onReset,
}: ConfigActionsProps) {
  return (
    <div className="config-actions">
      <button onClick={onReset} disabled={!dirty || saving}>
        Reset
      </button>
      <button className="primary" onClick={onSave} disabled={saving || !dirty}>
        {saving ? (
          <>
            <span className="spinner-small"></span>
            Saving...
          </>
        ) : (
          "Save"
        )}
      </button>
    </div>
  );
}
