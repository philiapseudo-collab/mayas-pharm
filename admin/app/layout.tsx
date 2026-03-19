import type { Metadata } from "next";
import { Fraunces, Manrope } from "next/font/google";
import { AppNav } from "../components/app-nav";
import "./globals.css";

const fraunces = Fraunces({
  subsets: ["latin"],
  variable: "--font-display",
  weight: ["600", "700"]
});

const manrope = Manrope({
  subsets: ["latin"],
  variable: "--font-body",
  weight: ["400", "500", "600", "700"]
});

export const metadata: Metadata = {
  title: "Maya's Pharm Admin",
  description: "Operations dashboard for Maya's Pharm WhatsApp ordering."
};

export default function RootLayout({
  children
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body className={`${fraunces.variable} ${manrope.variable}`}>
        <div className="background-mesh" />
        <div className="app-shell">
          <header className="hero">
            <div>
              <p className="eyebrow">Philia Technologies</p>
              <h1>Maya&apos;s Pharm Ops Console</h1>
              <p className="hero-copy">
                Manage pharmacist reviews, paid orders, stock visibility and Nairobi
                delivery settings from one lightweight dashboard.
              </p>
            </div>
            <div className="hero-chip">
              <span>WhatsApp-first</span>
              <span>Railway-ready</span>
              <span>Postgres + Redis</span>
            </div>
          </header>
          <AppNav />
          <main className="page-grid">{children}</main>
        </div>
      </body>
    </html>
  );
}
