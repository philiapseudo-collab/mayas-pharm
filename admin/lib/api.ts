export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL?.replace(/\/$/, "") || "http://localhost:8080";

async function parseResponse<T>(response: Response): Promise<T> {
  const contentType = response.headers.get("content-type") || "";
  const payload = contentType.includes("application/json")
    ? await response.json()
    : await response.text();

  if (!response.ok) {
    const message =
      typeof payload === "string"
        ? payload
        : (payload as { error?: string }).error || "Request failed";
    throw new Error(message);
  }

  return payload as T;
}

export async function apiGet<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    credentials: "include",
    cache: "no-store"
  });
  return parseResponse<T>(response);
}

export async function apiSend<T>(
  path: string,
  method: "POST" | "PUT" | "PATCH",
  body: unknown
): Promise<T> {
  const response = await fetch(`${API_BASE_URL}${path}`, {
    method,
    credentials: "include",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(body)
  });
  return parseResponse<T>(response);
}

export type OrderRow = {
  id: string;
  pickup_code?: string;
  status: string;
  customer_phone: string;
  total_amount: number;
  fulfillment_type?: string;
  delivery_zone_name?: string;
  review_required?: boolean;
  created_at?: string;
};

export type PrescriptionRow = {
  id: string;
  order_id: string;
  customer_phone: string;
  media_id: string;
  media_type: string;
  status: string;
  created_at?: string;
};

export type ProductRow = {
  id: string;
  name: string;
  category: string;
  price: number;
  stock_quantity: number;
  requires_prescription: boolean;
};

export type DeliveryZoneRow = {
  id: string;
  name: string;
  slug: string;
  fee: number;
  estimated_mins: number;
  is_active: boolean;
};

export type BusinessHourRow = {
  id: string;
  day_of_week: number;
  open_time: string;
  close_time: string;
  is_open: boolean;
};

export type StaffAccount = {
  id: string;
  name: string;
  role: string;
  bartender_code?: string;
};
