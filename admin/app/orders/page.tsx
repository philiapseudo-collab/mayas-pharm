"use client";

import { useEffect, useState } from "react";
import { DataShell } from "../../components/data-shell";
import { apiGet, apiSend, OrderRow } from "../../lib/api";

export default function OrdersPage() {
  const [orders, setOrders] = useState<OrderRow[]>([]);
  const [error, setError] = useState("");

  async function loadOrders() {
    try {
      setError("");
      const data = await apiGet<OrderRow[]>("/api/admin/orders?limit=50");
      setOrders(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load orders");
    }
  }

  useEffect(() => {
    void loadOrders();
  }, []);

  async function markOutForDelivery(orderID: string) {
    try {
      await apiSend(`/api/admin/orders/${orderID}/dispatch`, "POST", {});
      await loadOrders();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to dispatch order");
    }
  }

  return (
    <DataShell title="Live Orders" subtitle="Queue health across payment, packing and dispatch">
      {error ? <p className="danger-text">{error}</p> : null}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Code</th>
              <th>Status</th>
              <th>Phone</th>
              <th>Total</th>
              <th>Fulfilment</th>
              <th>Zone</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {orders.map((order) => (
              <tr key={order.id}>
                <td>{order.pickup_code || order.id.slice(0, 8)}</td>
                <td>
                  <span className="status">{order.status}</span>
                </td>
                <td>{order.customer_phone}</td>
                <td>KES {Math.round(order.total_amount || 0)}</td>
                <td>{order.fulfillment_type || "PICKUP"}</td>
                <td>{order.delivery_zone_name || "-"}</td>
                <td>
                  {order.status === "READY" ? (
                    <button className="secondary" onClick={() => void markOutForDelivery(order.id)}>
                      Mark Out For Delivery
                    </button>
                  ) : (
                    <span className="muted">No action</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </DataShell>
  );
}
