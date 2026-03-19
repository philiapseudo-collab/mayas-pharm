"use client";

import { useEffect, useState } from "react";
import { DataShell } from "../../components/data-shell";
import { apiGet, apiSend, PrescriptionRow } from "../../lib/api";

export default function PrescriptionsPage() {
  const [items, setItems] = useState<PrescriptionRow[]>([]);
  const [error, setError] = useState("");

  async function loadItems() {
    try {
      setError("");
      setItems(await apiGet<PrescriptionRow[]>("/api/admin/prescriptions"));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load prescriptions");
    }
  }

  useEffect(() => {
    void loadItems();
  }, []);

  async function review(id: string, decision: "APPROVED" | "REJECTED") {
    const notes = window.prompt(
      decision === "APPROVED"
        ? "Optional approval note"
        : "Reason for rejecting this prescription"
    );
    if (notes === null) {
      return;
    }
    try {
      await apiSend(`/api/admin/prescriptions/${id}/review`, "POST", {
        decision,
        notes
      });
      await loadItems();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to review prescription");
    }
  }

  return (
    <DataShell title="Pharmacist Queue" subtitle="Pending prescription uploads waiting for review">
      {error ? <p className="danger-text">{error}</p> : null}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Prescription</th>
              <th>Order</th>
              <th>Phone</th>
              <th>Media</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.id}>
                <td>{item.id.slice(0, 8)}</td>
                <td>{item.order_id.slice(0, 8)}</td>
                <td>{item.customer_phone}</td>
                <td>
                  <span className="pill">{item.media_type}</span>
                </td>
                <td>
                  <div className="inline-actions">
                    <button className="secondary" onClick={() => void review(item.id, "APPROVED")}>
                      Approve
                    </button>
                    <button className="warn" onClick={() => void review(item.id, "REJECTED")}>
                      Reject
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </DataShell>
  );
}
