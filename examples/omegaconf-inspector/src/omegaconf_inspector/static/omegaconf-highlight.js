window.highlightOmegaConf = function highlightOmegaConf(text) {
  const escaped = String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");

  return escaped
    .split("\n")
    .map((line) => {
      if (/^\s*#/.test(line)) {
        return `<span class="tok-comment">${line}</span>`;
      }
      let highlighted = line.replace(
        /(\$\{[^}]+\})/g,
        '<span class="tok-interp">$1</span>',
      );
      highlighted = highlighted.replace(
        /(\?\?\?)/g,
        '<span class="tok-missing">$1</span>',
      );
      highlighted = highlighted.replace(
        /^(\s*)([A-Za-z0-9_.-]+)(\s*:)/,
        '$1<span class="tok-key">$2</span>$3',
      );
      return highlighted;
    })
    .join("\n");
};
