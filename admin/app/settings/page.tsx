"use client";

import { useEffect, useState } from "react";
import { DataShell } from "../../components/data-shell";
import {
  apiGet,
  apiSend,
  BusinessHourRow,
  DeliveryZoneRow
} from "../../lib/api";

const dayNames = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export default function SettingsPage() {
  const [zones, setZones] = useState<DeliveryZoneRow[]>([]);
  const [hours, setHours] = useState<BusinessHourRow[]>([]);
  const [error, setError] = useState("");
  const [newZone, setNewZone] = useState({
    name: "",
    slug: "",
    fee: "",
    estimated_mins: ""
  });

  async function load() {
    try {
      setError("");
      const [zoneData, hourData] = await Promise.all([
        apiGet<DeliveryZoneRow[]>("/api/admin/delivery-zones"),
        apiGet<BusinessHourRow[]>("/api/admin/business-hours")
      ]);
      setZones(zoneData);
      setHours(hourData);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load settings");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function createZone(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      await apiSend("/api/admin/delivery-zones", "POST", {
        name: newZone.name,
        slug: newZone.slug,
        fee: Number(newZone.fee || 0),
        estimated_mins: Number(newZone.estimated_mins || 0),
        sort_order: zones.length + 1,
        is_active: true
      });
      setNewZone({ name: "", slug: "", fee: "", estimated_mins: "" });
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create delivery zone");
    }
  }

  async function saveHour(hour: BusinessHourRow) {
    try {
      await apiSend(`/api/admin/business-hours/${hour.id}`, "PUT", hour);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save business hours");
    }
  }

  return (
    <div className="two-col">
      <DataShell title="Delivery Zones" subtitle="Nairobi delivery pricing and travel-time defaults">
        {error ? <p className="danger-text">{error}</p> : null}
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Zone</th>
                <th>Fee</th>
                <th>ETA</th>
              </tr>
            </thead>
            <tbody>
              {zones.map((zone) => (
                <tr key={zone.id}>
                  <td>{zone.name}</td>
                  <td>KES {Math.round(zone.fee || 0)}</td>
                  <td>{zone.estimated_mins} mins</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <form className="form-stack" onSubmit={createZone}>
          <input
            placeholder="Zone name"
            value={newZone.name}
            onChange={(event) => setNewZone((current) => ({ ...current, name: event.target.value }))}
          />
          <input
            placeholder="slug"
            value={newZone.slug}
            onChange={(event) => setNewZone((current) => ({ ...current, slug: event.target.value }))}
          />
          <div className="toolbar">
            <input
              placeholder="Fee"
              value={newZone.fee}
              onChange={(event) => setNewZone((current) => ({ ...current, fee: event.target.value }))}
            />
            <input
              placeholder="ETA minutes"
              value={newZone.estimated_mins}
              onChange={(event) =>
                setNewZone((current) => ({ ...current, estimated_mins: event.target.value }))
              }
            />
          </div>
          <button type="submit">Add delivery zone</button>
        </form>
      </DataShell>

      <DataShell title="Business Hours" subtitle="Default 8am-10pm schedule used by the pharmacy workflow">
        <div className="form-stack">
          {hours.map((hour) => (
            <div key={hour.id} className="panel">
              <div className="panel-head">
                <div>
                  <p className="eyebrow">Day</p>
                  <h2>{dayNames[hour.day_of_week] || `Day ${hour.day_of_week}`}</h2>
                </div>
                <span className="pill">{hour.is_open ? "Open" : "Closed"}</span>
              </div>
              <div className="toolbar">
                <input
                  value={hour.open_time}
                  onChange={(event) =>
                    setHours((current) =>
                      current.map((item) =>
                        item.id === hour.id ? { ...item, open_time: event.target.value } : item
                      )
                    )
                  }
                />
                <input
                  value={hour.close_time}
                  onChange={(event) =>
                    setHours((current) =>
                      current.map((item) =>
                        item.id === hour.id ? { ...item, close_time: event.target.value } : item
                      )
                    )
                  }
                />
              </div>
              <div className="inline-actions">
                <button className="secondary" onClick={() => void saveHour(hour)}>
                  Save hours
                </button>
              </div>
            </div>
          ))}
        </div>
      </DataShell>
    </div>
  );
}
