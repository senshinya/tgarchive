import { Images } from 'lucide-preact';
import { useEffect } from 'preact/hooks';
import { useStore } from '../../state/store';
import { ViewHeader } from '../middle/ViewHeader';

export function MediaWall() {
  const store = useStore();
  useEffect(() => {
    void store.api.allMedia('all', 'all');
  }, []);
  return (
    <div id="MiddleColumn" class="ViewColumn">
      <ViewHeader icon={<Images size={22} />} title="媒体墙" status="" />
    </div>
  );
}
