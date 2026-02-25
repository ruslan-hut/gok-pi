import { Icon } from "./Icon";

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
        <Icon name="close" size={18} />
      </button>
    </div>
  );
}
