import { useEffect, useState } from "react";
import { api } from "./api";

/**
 * "Add a provider" form, opened by typing /provider add (with nothing after it) or by choosing
 * "Add a provider…" in the /provider list. "Fetch models" asks the server's /models (through goharness, so
 * keys held in environment variables never reach the browser) and offers the list to pick from.
 */
export function ProviderDialog({
  open,
  onClose,
  onAdded,
}: {
  open: boolean;
  onClose: () => void;
  onAdded: (name: string, model: string) => void;
}) {
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKeyEnv, setApiKeyEnv] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [models, setModels] = useState<string[] | null>(null);
  const [fetching, setFetching] = useState(false);
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setName("");
    setBaseUrl("");
    setModel("");
    setApiKeyEnv("");
    setApiKey("");
    setModels(null);
    setError(null);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const bothKeys = apiKey !== "" && apiKeyEnv.trim() !== "";
  const fetchModels = async () => {
    setError(null);
    if (bothKeys) return setError("Use a key variable or a key, not both.");
    setFetching(true);
    try {
      const r = await api.providerModels({
        baseUrl: baseUrl.trim(),
        apiKey: apiKey || undefined,
        apiKeyEnv: apiKeyEnv.trim() || undefined,
      });
      setModels(r.models);
    } catch (err) {
      setModels(null);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setFetching(false);
    }
  };
  const add = async () => {
    setError(null);
    if (bothKeys) return setError("Use a key variable or a key, not both.");
    setAdding(true);
    try {
      await api.createProvider({
        name: name.trim(),
        baseUrl: baseUrl.trim(),
        model: model.trim(),
        apiKey: apiKey || undefined,
        apiKeyEnv: apiKeyEnv.trim() || undefined,
      });
      onAdded(name.trim(), model.trim());
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setAdding(false);
    }
  };

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" aria-label="Add a provider" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>Add a provider</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
        <div className="modal-body">
          <p className="hint">Any OpenAI-compatible server: LiteLLM, vLLM, Ollama, LM Studio, a cloud API…</p>
          <label className="field">
            <span>Name</span>
            <input aria-label="Name" autoFocus placeholder="litellm" value={name} onChange={(e) => setName(e.target.value)} spellCheck={false} />
          </label>
          <label className="field">
            <span>Base URL</span>
            <input aria-label="Base URL" placeholder="http://172.16.10.46:4000/v1" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} spellCheck={false} />
          </label>
          <label className="field">
            <span>API key variable</span>
            <input aria-label="API key variable" placeholder="LITELLM_API_KEY (preferred; blank for no key)" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} spellCheck={false} />
          </label>
          <label className="field">
            <span>or an API key</span>
            <input aria-label="API key" type="password" placeholder="stored in ~/.config/goharness/custom_providers.json" value={apiKey} onChange={(e) => setApiKey(e.target.value)} spellCheck={false} />
          </label>
          <div className="row" style={{ gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <button className="btn btn-sm" onClick={fetchModels} disabled={fetching || !baseUrl.trim()}>
              {fetching ? "Fetching…" : "Fetch models"}
            </button>
            {models && (
              <select aria-label="Models on the server" value="" onChange={(e) => e.target.value && setModel(e.target.value)}>
                <option value="">{`Pick a model (${models.length})…`}</option>
                {models.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            )}
          </div>
          <label className="field">
            <span>Model</span>
            <input aria-label="Model" placeholder="the model to use first" value={model} onChange={(e) => setModel(e.target.value)} spellCheck={false} />
          </label>
          {error && (
            <div className="form-error" role="alert">
              {error}
            </div>
          )}
          <div className="row" style={{ gap: 8, marginTop: 10, justifyContent: "flex-end" }}>
            <button className="btn btn-sm" onClick={onClose}>
              Cancel
            </button>
            <button className="btn btn-sm btn-primary" onClick={add} disabled={adding || !name.trim() || !baseUrl.trim() || !model.trim()}>
              {adding ? "…" : "Add"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
