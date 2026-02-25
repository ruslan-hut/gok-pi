interface MessageBannerProps {
  message?: string;
  onDismiss: () => void;
}

export function MessageBanner({ message, onDismiss }: MessageBannerProps) {
  if (!message) return null;
  return (
    <div className="message-banner">
      {message}
      <button
        className="message-close"
        onClick={onDismiss}
        aria-label="Dismiss message"
      >
        &times;
      </button>
    </div>
  );
}
