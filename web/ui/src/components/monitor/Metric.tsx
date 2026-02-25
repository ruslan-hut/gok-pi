interface MetricProps {
  label: string;
  value: string;
}

export function Metric({ label, value }: MetricProps) {
  return (
    <div className="metric">
      <span>{label}</span>
      <span>{value}</span>
    </div>
  );
}
