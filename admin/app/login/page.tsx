"use client";

import { useEffect, useState } from "react";
import { apiGet, apiSend, StaffAccount } from "../../lib/api";

export default function LoginPage() {
  const [accounts, setAccounts] = useState<StaffAccount[]>([]);
  const [userID, setUserID] = useState("");
  const [pin, setPin] = useState("");
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    apiGet<StaffAccount[]>("/api/admin/auth/staff")
      .then((data) => {
        setAccounts(data);
        if (data[0]?.id) {
          setUserID(data[0].id);
        }
      })
      .catch((error) => setMessage(error.message));
  }, []);

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    setMessage("");
    try {
      await apiSend("/api/admin/auth/staff-login", "POST", { user_id: userID, pin });
      setMessage("Login successful. You can move to Orders or Prescription Queue.");
      setPin("");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Login failed");
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Staff Auth</p>
          <h2>PIN login for pharmacists and dispatchers</h2>
        </div>
      </div>
      <form className="form-stack" onSubmit={handleSubmit}>
        <label>
          Staff account
          <select value={userID} onChange={(event) => setUserID(event.target.value)}>
            {accounts.map((account) => (
              <option key={account.id} value={account.id}>
                {account.name} ({account.role})
              </option>
            ))}
          </select>
        </label>
        <label>
          Four-digit PIN
          <input
            type="password"
            inputMode="numeric"
            maxLength={4}
            value={pin}
            onChange={(event) => setPin(event.target.value)}
            placeholder="1234"
          />
        </label>
        <div className="inline-actions">
          <button type="submit" disabled={loading}>
            {loading ? "Signing in..." : "Sign in"}
          </button>
        </div>
        {message ? (
          <p className={message.toLowerCase().includes("successful") ? "muted" : "danger-text"}>
            {message}
          </p>
        ) : null}
      </form>
    </section>
  );
}
