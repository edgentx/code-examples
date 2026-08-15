import type { ReactNode } from 'react';

/**
 * An icon-first control.
 *
 * The icon is decorative and hidden from assistive technology; the accessible
 * name comes from `label`, which is also the text of a tooltip shown on hover
 * *and* on keyboard focus. A tooltip that appears only on hover is a tooltip a
 * keyboard user never sees, and the `title` attribute alone is not shown on
 * focus at all, which is why this component draws its own.
 */
export function IconButton({
  icon,
  label,
  onClick,
  disabled,
  tone = 'quiet',
  type = 'button',
  children,
}: {
  icon: string;
  label: string;
  onClick?: () => void;
  disabled?: boolean;
  tone?: 'quiet' | 'primary' | 'danger';
  type?: 'button' | 'submit';
  children?: ReactNode;
}) {
  return (
    <span className="icon-button">
      <button type={type} className={`icon-button__control icon-button__control--${tone}`}
        onClick={onClick} disabled={disabled} aria-label={label}>
        <span className="icon-button__glyph" aria-hidden="true">{icon}</span>
        {children}
      </button>
      {/* Visual only: the accessible name is the button's aria-label, so the
          tooltip must not be announced a second time. */}
      <span className="icon-button__tooltip" aria-hidden="true">
        {label}
      </span>
    </span>
  );
}
