// navigator.clipboard only exists in a secure context (HTTPS, or localhost).
// This admin panel is frequently reached over a plain http:// LAN address
// (e.g. a self-hosted server's own IP), where navigator.clipboard is simply
// undefined -- calling .writeText on it throws "Cannot read properties of
// undefined". Fall back to the old execCommand('copy') path via a hidden,
// off-screen textarea, which still works in that case.
export async function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.top = "-1000px";
  textarea.style.left = "-1000px";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  try {
    const ok = document.execCommand("copy");
    if (!ok) {
      throw new Error("Copy command was not successful");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}
