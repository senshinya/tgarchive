import './ui.scss';

interface Props {
  checked: boolean;
  label: string;
  onChange: (checked: boolean) => void;
}

export function Checkbox({ checked, label, onChange }: Props) {
  return (
    <label class="Checkbox">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.currentTarget as HTMLInputElement).checked)} />
      <span class="Checkbox-box" aria-hidden="true" />
      <span class="Checkbox-label">{label}</span>
    </label>
  );
}
