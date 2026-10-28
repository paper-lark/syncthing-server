(() => {
  const bindClose = (dialog, getOpener) => {
    dialog.querySelector("[data-close-dialog]").addEventListener("click", () => dialog.close());
    dialog.addEventListener("click", (event) => {
      const bounds = dialog.getBoundingClientRect();
      if (event.target === dialog && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) {
        dialog.close();
      }
    });
    dialog.addEventListener("close", () => {
      const opener = getOpener();
      if (opener?.isConnected) opener.focus();
    });
  };

  const accountFallback = document.querySelector(".account-fallback");
  const accountDialog = document.getElementById("account-dialog");
  if (accountFallback && accountDialog && typeof accountDialog.showModal === "function") {
    accountDialog.append(accountFallback.querySelector(".account-panel"));
    const trigger = accountFallback.querySelector("summary");
    trigger.addEventListener("click", (event) => {
      event.preventDefault();
      accountDialog.showModal();
      accountDialog.querySelector("[data-close-dialog]").focus();
    });
    bindClose(accountDialog, () => trigger);
  }

  const deleteDialog = document.getElementById("delete-dialog");
  const deleteTriggers = document.querySelectorAll(".delete-trigger");
  if (deleteDialog && deleteTriggers.length && typeof deleteDialog.showModal === "function") {
    let opener;
    let fallback;
    let form;
    for (const trigger of deleteTriggers) {
      trigger.addEventListener("click", () => {
        opener = trigger;
        fallback = trigger.nextElementSibling;
        form = fallback.querySelector(".danger-form");
        deleteDialog.querySelector("h2").textContent = `Delete folder: ${trigger.closest("tr").querySelector(".cell-title").textContent}`;
        deleteDialog.append(form);
        deleteDialog.showModal();
        form.querySelector('input[name="confirm"]').focus();
      });
    }
    deleteDialog.addEventListener("close", () => {
      form.reset();
      fallback.append(form);
    });
    bindClose(deleteDialog, () => opener);
    document.documentElement.classList.add("delete-modal-ready");
  }

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
    bindClose(dialog, () => opener);

    if (location.hash === `#${fallbackID}` && triggers.length) open(triggers[0]);
  }

  document.documentElement.classList.add("modal-ready");
})();
