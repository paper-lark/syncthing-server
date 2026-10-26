(() => {
  const entries = [
    ["create-folder", "folder-dialog"],
    ["add-peer", "peer-dialog"],
  ];

  if (!entries.every(([fallbackID, dialogID]) => {
    const fallback = document.getElementById(fallbackID);
    const dialog = document.getElementById(dialogID);
    return fallback && dialog && typeof dialog.showModal === "function";
  })) {
    return;
  }

  for (const [fallbackID, dialogID] of entries) {
    const fallback = document.getElementById(fallbackID);
    const dialog = document.getElementById(dialogID);
    const panel = fallback.querySelector(".create-panel");
    dialog.append(panel);
    let opener;

    const open = (trigger) => {
      opener = trigger;
      dialog.showModal();
      panel.querySelector('input:not([type="hidden"])').focus();
    };

    const triggers = document.querySelectorAll(`a[href="#${fallbackID}"]`);
    for (const trigger of triggers) {
      trigger.addEventListener("click", (event) => {
        event.preventDefault();
        open(trigger);
      });
    }
    dialog.querySelector("[data-close-dialog]").addEventListener("click", () => dialog.close());
    dialog.addEventListener("click", (event) => {
      const bounds = dialog.getBoundingClientRect();
      if (event.target === dialog && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) {
        dialog.close();
      }
    });
    dialog.addEventListener("close", () => {
      if (opener?.isConnected) opener.focus();
    });

    if (location.hash === `#${fallbackID}` && triggers.length) open(triggers[0]);
  }

  document.documentElement.classList.add("modal-ready");
})();
