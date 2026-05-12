/** Edge-kind legend overlay for the React Flow canvas. */

const KINDS = [
  { label: 'Normal', color: '#64748b', dash: undefined },
  { label: 'Fallback', color: '#a855f7', dash: '6 3' },
  { label: 'Failure', color: '#ef4444', dash: undefined },
  { label: 'Recovery', color: '#10b981', dash: '4 2' },
]

export function Legend() {
  return (
    <div className="legend">
      <div className="legend__title">Edge kinds</div>
      {KINDS.map(({ label, color, dash }) => (
        <div key={label} className="legend__row">
          <svg width="28" height="12" className="legend__swatch">
            <line
              x1="2"
              y1="6"
              x2="26"
              y2="6"
              stroke={color}
              strokeWidth="2"
              strokeDasharray={dash}
            />
            <polygon points="22,3 28,6 22,9" fill={color} />
          </svg>
          <span className="legend__label">{label}</span>
        </div>
      ))}
    </div>
  )
}
