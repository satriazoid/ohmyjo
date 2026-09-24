import { useApp } from "../store";

/** Transient toasts for backend errors and config warnings. */
export function Notices() {
  const notices = useApp((s) => s.notices);
  const dismiss = useApp((s) => s.dismiss);
  if (notices.length === 0) return null;
  return (
    <div className="notices">
      {notices.map((n) => (
        <div key={n.id} className={`notice notice-${n.level}`}>
          <span>{n.text}</span>
          <button type="button" title="Dismiss" onClick={() => dismiss(n.id)}>
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
