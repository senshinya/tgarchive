import { ArrowLeft } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { navigate } from '../../lib/router';
import { IconButton } from '../../ui/Button';

/** Leaves a middle-column view: back to the list when it was opened from there, else home. */
export function goBackToList() {
  const state = history.state as { fromList?: boolean } | null;
  if (state?.fromList) history.back();
  else navigate({ name: 'home' });
}

/** The header of a view that takes the middle column (media wall, stats): back, icon, title. */
export function ViewHeader(props: { icon: ComponentChildren; title: string; status: string; actions?: ComponentChildren }) {
  return (
    <div class="MiddleHeader">
      <IconButton label="返回" class="back-button" onClick={goBackToList}>
        <ArrowLeft size={24} />
      </IconButton>
      <span class="MiddleHeader-info ViewHeader">
        <span class="ViewHeader-icon">{props.icon}</span>
        <span class="MiddleHeader-text">
          <span class="MiddleHeader-title">{props.title}</span>
          <span class="MiddleHeader-status">{props.status}</span>
        </span>
      </span>
      {props.actions}
    </div>
  );
}
