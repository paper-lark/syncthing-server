(() => {
  for (const button of document.querySelectorAll("[data-copy-id]")) {
    const tooltip = document.createElement("span");
    tooltip.className = "copy-tooltip";
    tooltip.setAttribute("role", "status");
    button.append(tooltip);
    let timeout;
    button.addEventListener("click", async () => {
      const id = button.parentElement.querySelector("[data-device-id]").textContent;
      try {
        await navigator.clipboard.writeText(id);
        clearTimeout(timeout);
        tooltip.textContent = "Copied";
        timeout = setTimeout(() => { tooltip.textContent = ""; }, 1800);
      } catch (_) {
        // Keep the confirmation hidden when clipboard access fails.
      }
    });
  }

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

  const bindDangerDialog = (dialogID, triggerClass, readyClass, title, focusSelector) => {
    const dialog = document.getElementById(dialogID);
    const triggers = document.querySelectorAll(`.${triggerClass}`);
    if (!dialog || !triggers.length || typeof dialog.showModal !== "function") return;

    let opener;
    let fallback;
    let form;
    for (const trigger of triggers) {
      trigger.addEventListener("click", () => {
        opener = trigger;
        fallback = trigger.nextElementSibling;
        form = fallback.querySelector(".danger-form");
        dialog.querySelector("h2").textContent = `${title}: ${trigger.closest("tr").querySelector(".cell-title").textContent}`;
        dialog.append(form);
        dialog.showModal();
        (form.querySelector(focusSelector) || form.querySelector('button[type="submit"]')).focus();
      });
    }
    dialog.addEventListener("close", () => {
      form.reset();
      fallback.append(form);
    });
    bindClose(dialog, () => opener);
    document.documentElement.classList.add(readyClass);
  };

  bindDangerDialog("delete-dialog", "delete-trigger", "delete-modal-ready", "Delete folder", 'input[name="confirm"]');
  bindDangerDialog("remove-dialog", "remove-trigger", "remove-modal-ready", "Remove device", 'button[type="submit"]');

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
