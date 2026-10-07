import { ChartColumn } from 'lucide-preact';
import { useEffect } from 'preact/hooks';
import { useStore } from '../../state/store';
import { ViewHeader } from '../middle/ViewHeader';

export function StatsView() {
  const store = useStore();
  useEffect(() => {
    void store.api.stats(-new Date().getTimezoneOffset());
  }, []);
  return (
    <div id="MiddleColumn" class="ViewColumn">
      <ViewHeader icon={<ChartColumn size={22} />} title="统计" status="" />
    </div>
  );
}
