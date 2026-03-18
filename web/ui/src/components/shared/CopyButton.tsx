import { useState, useCallback } from "react";
import { Icon } from "./Icon";

interface CopyButtonProps {
  getText: () => string;
}

export function CopyButton({ getText }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    const text = getText();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore
    }
  }, [getText]);

  return (
    <button
      type="button"
      className="copy-btn"
      onClick={handleCopy}
      title={copied ? "Copied!" : "Copy to clipboard"}
    >
      <Icon name={copied ? "check" : "content_copy"} size={14} />
    </button>
  );
}
