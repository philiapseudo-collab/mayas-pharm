import Link from "next/link";

const cards = [
  {
    href: "/orders" as const,
    title: "Orders",
    body: "Track paid, packing, ready and delivery-stage orders in one place."
  },
  {
    href: "/prescriptions" as const,
    title: "Prescription Queue",
    body: "Approve or reject pharmacist-review orders without leaving the dashboard."
  },
  {
    href: "/catalog" as const,
    title: "Catalog",
    body: "Browse the seeded pharmacy catalog, stock, and prescription flags."
  },
  {
    href: "/settings" as const,
    title: "Settings",
    body: "Review Nairobi delivery zones and business hours used by the WhatsApp bot."
  }
];

export default function HomePage() {
  return (
    <>
      <section className="stats-grid">
        {cards.map((card) => (
          <Link key={card.href} href={card.href} className="stat-card">
            <p className="eyebrow">Launch Surface</p>
            <h3>{card.title}</h3>
            <p>{card.body}</p>
          </Link>
        ))}
      </section>

      <section className="two-col">
        <article className="panel">
          <p className="eyebrow">Why This Shape</p>
          <h2>Small enough to ship, opinionated enough to operate.</h2>
          <p>
            The customer experience stays on WhatsApp. This admin surface is only for
            staff actions that need auditability: reviewing prescriptions, watching
            queue states, and maintaining delivery settings.
          </p>
        </article>

        <article className="panel">
          <p className="eyebrow">Deployment</p>
          <h2>Run as a separate Railway service.</h2>
          <p>
            Set the admin working directory to <code>admin</code> and point{" "}
            <code>NEXT_PUBLIC_API_BASE_URL</code> at the Go API service.
          </p>
          <div className="callout">
            Use the same domain family as the API so auth cookies and webhook review
            actions stay predictable.
          </div>
        </article>
      </section>
    </>
  );
}
