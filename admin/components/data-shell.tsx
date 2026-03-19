import { ReactNode } from "react";

type DataShellProps = {
  title: string;
  subtitle: string;
  children: ReactNode;
};

export function DataShell({ title, subtitle, children }: DataShellProps) {
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">{title}</p>
          <h2>{subtitle}</h2>
        </div>
      </div>
      {children}
    </section>
  );
}
