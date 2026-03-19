"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/" as const, label: "Overview" },
  { href: "/orders" as const, label: "Orders" },
  { href: "/prescriptions" as const, label: "Prescriptions" },
  { href: "/catalog" as const, label: "Catalog" },
  { href: "/settings" as const, label: "Settings" },
  { href: "/login" as const, label: "Login" }
];

export function AppNav() {
  const pathname = usePathname();

  return (
    <nav className="app-nav">
      {links.map((link) => {
        const active = pathname === link.href;
        return (
          <Link
            key={link.href}
            href={link.href}
            className={active ? "nav-link active" : "nav-link"}
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}
