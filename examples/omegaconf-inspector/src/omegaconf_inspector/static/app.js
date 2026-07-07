const state = {
  projects: [],
  project: null,
  files: [],
  activeFileId: null,
  replacingFileId: null,
  dirty: false,
};

const DEMO_PROJECT_NAME = "Demo project";
const DEMO_LAYERS = [
  {
    name: "base.yaml",
    content: "server:\n  host: localhost\n  port: 8076\npaths:\n  cache: /tmp/cache\n",
  },
  {
    name: "override.yaml",
    content: "server:\n  port: ${oc.env:PORT,8076}\nmessage: ${server.host}:${server.port}\n",
  },
];

const $ = (id) => document.getElementById(id);
let liveMergeTimer = null;

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!response.ok) {
    const detail = await response.text();
    throw new Error(detail || response.statusText);
  }
  return response.json();
}

async function loadHealth() {
  try {
    const health = await api("/_health_");
    $("health").textContent = `healthy ${health.version}`;
    $("health").classList.remove("fail");
  } catch (error) {
    $("health").textContent = "health failed";
    $("health").classList.add("fail");
  }
}

async function loadProjects() {
  state.projects = await api("/api/projects");
  renderProjects();
  if (state.project && !state.projects.some((project) => project.id === state.project.id)) {
    state.project = null;
  }
  if (!state.project && state.projects.length) {
    await selectProject(state.projects[0].id);
  } else if (!state.projects.length) {
    clearProject();
  }
}

function renderProjects() {
  $("projects").innerHTML = "";
  updateProjectControls();
  for (const project of state.projects) {
    const item = document.createElement("button");
    item.className = `item ${state.project?.id === project.id ? "active" : ""}`;
    item.innerHTML = `<div class="item-title">${escapeHtml(project.name)}</div><div class="item-subtitle">${project.updated_at}</div>`;
    item.addEventListener("click", () => selectProject(project.id));
    $("projects").append(item);
  }
}

async function selectProject(id) {
  state.project = await api(`/api/projects/${id}`);
  state.files = [...state.project.files].sort((a, b) => a.order - b.order);
  if (!state.files.some((file) => file.id === state.activeFileId)) {
    state.activeFileId = state.files[0]?.id || null;
  }
  $("project-name").value = state.project.name;
  state.dirty = false;
  renderProjects();
  renderLayers();
  clearOutput();
  updateProjectControls();
}

function clearProject() {
  state.project = null;
  state.files = [];
  state.activeFileId = null;
  state.replacingFileId = null;
  state.dirty = false;
  $("project-name").value = "";
  $("input-tabs").innerHTML = "";
  $("layer-editor").innerHTML = "";
  renderNoProjectMessage();
  $("path-query").value = "";
  clearOutput();
  updateMergeButton();
  updatePathClearButton();
  updateProjectControls();
}

function renderNoProjectMessage() {
  const empty = document.createElement("div");
  empty.className = "empty-pane";
  empty.textContent = "No project selected.";
  $("layer-editor").append(empty);
}

function renderLayers() {
  const tabs = $("input-tabs");
  const editor = $("layer-editor");
  tabs.innerHTML = "";
  editor.innerHTML = "";
  updateMergeButton();

  if (!state.files.length) {
    tabs.append(addLayerButton());
    const empty = document.createElement("div");
    empty.className = "empty-pane";
    empty.textContent = "No input configs yet.";
    editor.append(empty);
    return;
  }

  if (!state.files.some((file) => file.id === state.activeFileId)) {
    state.activeFileId = state.files[0].id;
  }

  for (const file of state.files) {
    const tab = document.createElement("div");
    tab.className = `layer-tab ${file.id === state.activeFileId ? "active" : ""}`;
    tab.draggable = true;
    tab.title = file.name;
    const tabMain = document.createElement("div");
    tabMain.className = "layer-tab-main";
    const select = document.createElement("button");
    select.type = "button";
    select.className = "layer-tab-select";
    select.textContent = file.name;
    select.title = file.name;
    select.addEventListener("click", () => {
      state.activeFileId = file.id;
      renderLayers();
    });
    const load = document.createElement("button");
    load.type = "button";
    load.className = "layer-tab-icon layer-tab-load";
    load.textContent = "📂";
    load.title = `Replace ${file.name}`;
    load.setAttribute("aria-label", `Replace ${file.name}`);
    load.addEventListener("click", (event) => {
      event.stopPropagation();
      replaceLayerFromFile(file.id);
    });
    const close = document.createElement("button");
    close.type = "button";
    close.className = "layer-tab-icon layer-tab-close";
    close.textContent = "×";
    close.title = `Delete ${file.name}`;
    close.setAttribute("aria-label", `Delete ${file.name}`);
    close.addEventListener("click", (event) => {
      event.stopPropagation();
      if (!confirm(`Remove ${file.name}?`)) return;
      deleteLayer(file.id);
    });
    tabMain.append(select, load, close);
    tab.append(tabMain);
    tab.addEventListener("dragstart", (event) => {
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", file.id);
    });
    tab.addEventListener("dragover", (event) => {
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
    });
    tab.addEventListener("drop", (event) => {
      event.preventDefault();
      const rect = tab.getBoundingClientRect();
      const beforeTarget = event.clientX < rect.left + rect.width / 2;
      moveLayerNear(event.dataTransfer.getData("text/plain"), file.id, beforeTarget);
    });
    tabs.append(tab);
  }
  tabs.append(addLayerButton());

  const file = state.files.find((entry) => entry.id === state.activeFileId);
  if (file) {
    const layer = document.createElement("div");
    layer.className = "layer active-layer";
    layer.innerHTML = `
      <div class="layer-head">
        <input type="checkbox" class="layer-enabled" ${file.enabled ? "checked" : ""} />
        <input class="layer-name" value="${escapeAttr(file.name)}" title="${escapeAttr(file.name)}" placeholder="config.yaml" />
        <button class="delete-layer icon-button danger-button" title="Delete layer" aria-label="Delete layer">×</button>
      </div>
      <textarea spellcheck="false">${escapeHtml(file.content)}</textarea>
    `;
    layer.querySelector(".layer-enabled").addEventListener("change", (event) => {
      file.enabled = event.target.checked;
      state.dirty = true;
    });
    layer.querySelector(".layer-name").addEventListener("input", (event) => {
      file.name = event.target.value;
      state.dirty = true;
    });
    layer.querySelector("textarea").addEventListener("input", (event) => {
      file.content = event.target.value;
      state.dirty = true;
    });
    layer.querySelector(".delete-layer").addEventListener("click", () => deleteLayer(file.id));
    editor.append(layer);
  }
}

function addLayerButton() {
  const button = document.createElement("button");
  button.id = "add-layer";
  button.type = "button";
  button.className = "icon-button add-layer-tab";
  button.title = "Add empty config layer";
  button.setAttribute("aria-label", "Add empty config layer");
  button.textContent = "+";
  button.addEventListener("click", () => addLayer());
  return button;
}

async function saveFiles() {
  if (!state.project) return;
  for (let index = 0; index < state.files.length; index += 1) {
    const file = state.files[index];
    file.order = index;
    if (file.id.startsWith("local-")) {
      const oldId = file.id;
      const saved = await api(`/api/projects/${state.project.id}/files`, {
        method: "POST",
        body: JSON.stringify(file),
      });
      file.id = saved.id;
      if (state.activeFileId === oldId) {
        state.activeFileId = saved.id;
      }
    } else {
      await api(`/api/projects/${state.project.id}/files/${file.id}`, {
        method: "PUT",
        body: JSON.stringify(file),
      });
    }
  }
  state.dirty = false;
  await selectProject(state.project.id);
}

async function ensureSaved() {
  if (state.dirty) {
    await saveFiles();
  }
}

function addLayer(name = "config.yaml", content = "") {
  if (!state.project) return;
  const id = `local-${crypto.randomUUID()}`;
  state.files.push({
    id,
    project_id: state.project?.id,
    name,
    content,
    order: state.files.length,
    enabled: true,
  });
  state.activeFileId = id;
  state.dirty = true;
  renderLayers();
}

async function deleteProject() {
  if (!state.project) return;
  await api(`/api/projects/${state.project.id}`, { method: "DELETE" });
  state.project = null;
  state.files = [];
  state.activeFileId = null;
  await loadProjects();
}

function moveLayerNear(draggedId, targetId, beforeTarget) {
  if (!draggedId || draggedId === targetId) return;
  const draggedIndex = state.files.findIndex((file) => file.id === draggedId);
  const targetIndex = state.files.findIndex((file) => file.id === targetId);
  if (draggedIndex < 0 || targetIndex < 0) return;
  const [dragged] = state.files.splice(draggedIndex, 1);
  const adjustedTarget = state.files.findIndex((file) => file.id === targetId);
  state.files.splice(beforeTarget ? adjustedTarget : adjustedTarget + 1, 0, dragged);
  state.activeFileId = draggedId;
  state.dirty = true;
  renderLayers();
}

async function deleteLayer(id) {
  const index = state.files.findIndex((entry) => entry.id === id);
  const file = state.files.find((entry) => entry.id === id);
  state.files = state.files.filter((entry) => entry.id !== id);
  if (state.activeFileId === id) {
    state.activeFileId = state.files[Math.min(index, state.files.length - 1)]?.id || null;
  }
  if (file && !file.id.startsWith("local-")) {
    await api(`/api/projects/${state.project.id}/files/${file.id}`, { method: "DELETE" });
    await selectProject(state.project.id);
  } else {
    state.dirty = true;
    renderLayers();
  }
}

async function merge() {
  if (!state.project || !state.files.length) return;
  updatePathClearButton();
  try {
    await ensureSaved();
    const result = await api(`/api/projects/${state.project.id}/merge`, {
      method: "POST",
      body: JSON.stringify({
        path: $("path-query").value || null,
      }),
    });
    renderOutput(result);
  } catch (error) {
    renderError(error);
  }
}

function scheduleLiveMerge(delay = 200) {
  updatePathClearButton();
  if (!state.project || !state.files.length) return;
  clearTimeout(liveMergeTimer);
  liveMergeTimer = setTimeout(() => {
    merge();
  }, delay);
}

function clearPathLookup() {
  $("path-query").value = "";
  scheduleLiveMerge(0);
}

function renderOutput(result) {
  const emptyMessage = result.ok === false ? "# Merge failed.\n" : "# No output.\n";
  $("merged").innerHTML = highlightOmegaConf(formatOutput(result, result.merged_yaml || emptyMessage));
  $("resolved").innerHTML = highlightOmegaConf(formatOutput(result, result.resolved_yaml || emptyMessage));
  showTab("merged");
}

function renderError(error) {
  const message = error?.stack || error?.message || String(error);
  $("merged").textContent = `# Request failed.\n# ${message.replaceAll("\n", "\n# ")}\n`;
  $("resolved").textContent = "";
  showTab("merged");
}

function formatOutput(result, yaml) {
  const lines = [];
  if (result.error) {
    lines.push(`# Error: ${String(result.error).replaceAll("\n", "\n# ")}`, "");
  }
  lines.push(yaml);
  return lines.join("\n");
}

async function newProject() {
  const firstProject = state.projects.length === 0;
  const project = await api("/api/projects", {
    method: "POST",
    body: JSON.stringify({ name: firstProject ? DEMO_PROJECT_NAME : nextProjectName() }),
  });
  if (firstProject) {
    await createDemoLayers(project.id);
  }
  await loadProjects();
  await selectProject(project.id);
}

async function createDemoLayers(projectId) {
  for (let index = 0; index < DEMO_LAYERS.length; index += 1) {
    const layer = DEMO_LAYERS[index];
    await api(`/api/projects/${projectId}/files`, {
      method: "POST",
      body: JSON.stringify({
        name: layer.name,
        content: layer.content,
        order: index,
        enabled: true,
      }),
    });
  }
}

function nextProjectName() {
  const used = new Set(state.projects.map((project) => project.name));
  for (let index = 1; ; index += 1) {
    const candidate = `Project ${index}`;
    if (!used.has(candidate)) return candidate;
  }
}

async function saveProject() {
  if (!state.project) return;
  await api(`/api/projects/${state.project.id}`, {
    method: "PUT",
    body: JSON.stringify({ name: $("project-name").value, ui_state: {} }),
  });
  await saveFiles();
  await loadProjects();
}

function setupTabs() {
  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => showTab(tab.dataset.tab));
  }
}

function showTab(name) {
  document.querySelectorAll(".tab").forEach((entry) => {
    entry.classList.toggle("active", entry.dataset.tab === name);
  });
  document.querySelectorAll(".output").forEach((entry) => {
    entry.classList.toggle("active", entry.id === name);
  });
}

function setupFilePicker() {
  $("file-picker").addEventListener("change", async (event) => {
    if (!state.project) {
      event.target.value = "";
      return;
    }
    for (const file of event.target.files) {
      addLayer(file.name, await file.text());
    }
    event.target.value = "";
  });
  $("replace-file-picker").addEventListener("change", async (event) => {
    const selected = event.target.files[0];
    if (selected && state.replacingFileId) {
      replaceLayer(state.replacingFileId, selected.name, await selected.text());
    }
    state.replacingFileId = null;
    event.target.value = "";
  });
}

function replaceLayerFromFile(fileId) {
  state.replacingFileId = fileId;
  $("replace-file-picker").click();
}

function replaceLayer(fileId, name, content) {
  const file = state.files.find((entry) => entry.id === fileId);
  if (!file) return;
  file.name = name;
  file.content = content;
  state.activeFileId = fileId;
  state.dirty = true;
  renderLayers();
}

function updateMergeButton() {
  $("merge").disabled = !state.project || !state.files.length;
}

function updatePathClearButton() {
  $("clear-path").disabled = !$("path-query").value;
}

function updateProjectControls() {
  const hasProject = Boolean(state.project);
  $("delete-project").disabled = !hasProject;
  $("project-name").disabled = !hasProject;
  $("save-project").disabled = !hasProject;
  $("file-picker").disabled = !hasProject;
}

function clearOutput() {
  $("merged").textContent = "";
  $("resolved").textContent = "";
  showTab("merged");
}

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeAttr(value) {
  return escapeHtml(value).replaceAll('"', "&quot;");
}

async function start() {
  setupTabs();
  setupFilePicker();
  $("new-project").addEventListener("click", newProject);
  $("delete-project").addEventListener("click", deleteProject);
  $("save-project").addEventListener("click", saveProject);
  $("merge").addEventListener("click", merge);
  $("path-query").addEventListener("input", () => scheduleLiveMerge());
  $("clear-path").addEventListener("click", clearPathLookup);
  updatePathClearButton();
  await loadHealth();
  await loadProjects();
}

start().catch((error) => {
  $("merged").textContent = `# Startup failed.\n# ${String(error.stack || error).replaceAll("\n", "\n# ")}\n`;
});
