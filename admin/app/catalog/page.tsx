"use client";

import { useEffect, useMemo, useState } from "react";
import { DataShell } from "../../components/data-shell";
import { apiGet, ProductRow } from "../../lib/api";

export default function CatalogPage() {
  const [products, setProducts] = useState<ProductRow[]>([]);
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    apiGet<ProductRow[]>("/api/admin/products")
      .then(setProducts)
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load catalog"));
  }, []);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) {
      return products;
    }
    return products.filter((product) => {
      return (
        product.name.toLowerCase().includes(normalized) ||
        product.category.toLowerCase().includes(normalized)
      );
    });
  }, [products, query]);

  return (
    <DataShell title="Catalog" subtitle="Stock visibility across the seeded pharmacy range">
      <div className="toolbar">
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search product or category"
        />
      </div>
      {error ? <p className="danger-text">{error}</p> : null}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Product</th>
              <th>Category</th>
              <th>Price</th>
              <th>Stock</th>
              <th>RX</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((product) => (
              <tr key={product.id}>
                <td>{product.name}</td>
                <td>{product.category}</td>
                <td>KES {Math.round(product.price || 0)}</td>
                <td>{product.stock_quantity}</td>
                <td>{product.requires_prescription ? "Yes" : "No"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </DataShell>
  );
}
