import { useId } from 'preact/hooks';
import './ui.scss';

interface Props {
  label: string;
  value: string;
  onInput: (value: string) => void;
  error?: string;
  type?: 'text' | 'password' | 'tel';
  inputMode?: 'text' | 'numeric' | 'tel';
  autoComplete?: string;
  disabled?: boolean;
  autoFocus?: boolean;
}

/** Web A style text field: 48px, 16px radius, floating label that shows the error text. */
export function InputField({ label, value, onInput, error, type = 'text', inputMode, autoComplete = 'off', disabled, autoFocus }: Props) {
  const id = useId();
  return (
    <div class={`InputField${error ? ' error' : ''}${value ? ' touched' : ''}`}>
      <input
        id={id}
        type={type as 'text' /* preact 11 ARIA typing narrows type per role */}
        value={value}
        inputMode={inputMode}
        autoComplete={autoComplete}
        disabled={disabled}
        autoFocus={autoFocus}
        placeholder=" "
        aria-invalid={error ? true : undefined}
        onInput={(e) => onInput((e.currentTarget as HTMLInputElement).value)}
      />
      <label for={id}>{error || label}</label>
    </div>
  );
}
