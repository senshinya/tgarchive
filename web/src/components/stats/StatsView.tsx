import { ChartColumn, RefreshCw } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { Stats } from '../../api/types';
import { chatName, formatSize } from '../../lib/format';
import { convRoute, navigate } from '../../lib/router';
import { cumulative, dailyCumulative, heatmapGrid, localToday, quantileLevels, shiftDay } from '../../lib/stats';
import { useStore } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { ViewHeader } from '../middle/ViewHeader';
import { Wallpaper } from '../middle/Wallpaper';
import './stats.scss';

const KIND_LABELS: Record<string, string> = {
  photo: '图片',
  video: '视频',
  animation: 'GIF',
  document: '文件',
  audio: '音频',
  voice: '语音',
  video_note: '圆形视频',
  sticker: '贴纸',
  other: '其他（缩略图等）',
};

const num = (n: number) => n.toLocaleString('en-US');

function Card({ title, children, class: cls = '' }: { title: string; children: ComponentChildren; class?: string }) {
  return (
    <section class={`StatsCard ${cls}`}>
      <h3 class="StatsCard-title">{title}</h3>
      {children}
    </section>
  );
}

function Overview({ t }: { t: Stats['totals'] }) {
  const used = t.disk_total > 0 ? (t.disk_total - t.disk_free) / t.disk_total : 0;
  return (
    <div class="StatsOverview">
      <div class="StatsFigure">
        <span class="StatsFigure-label">消息</span>
        <span class="StatsFigure-value">{num(t.messages)}</span>
        <span class="StatsFigure-note">{`私聊 ${t.private_chats} · 频道 ${t.channel_chats}`}</span>
      </div>
      <div class="StatsFigure">
        <span class="StatsFigure-label">媒体</span>
        <span class="StatsFigure-value">{formatSize(t.media_bytes)}</span>
        <span class="StatsFigure-note">{`${num(t.media_files)} 个文件`}</span>
      </div>
      <div class="StatsFigure">
        <span class="StatsFigure-label">数据库</span>
        <span class="StatsFigure-value">{formatSize(t.db_bytes)}</span>
        <span class="StatsFigure-note">SQLite</span>
      </div>
      {t.disk_total > 0 && (
        <div class="StatsFigure">
          <span class="StatsFigure-label">数据盘</span>
          <span class="StatsFigure-value">{`${Math.round(used * 100)}%`}</span>
          <span class="StatsBar">
            <span class={`StatsBar-fill${used > 0.9 ? ' danger' : ''}`} style={{ width: `${used * 100}%` }} />
          </span>
          <span class="StatsFigure-note">{`剩余 ${formatSize(t.disk_free)} / 共 ${formatSize(t.disk_total)}`}</span>
        </div>
      )}
    </div>
  );
}

function Heatmap({ daily }: { daily: Stats['daily'] }) {
  const today = localToday();
  const grid = useMemo(() => heatmapGrid(daily, today), [daily, today]);
  const level = useMemo(() => quantileLevels(daily.map((d) => d.count)), [daily]);
  const [picked, setPicked] = useState<{ day: string; count: number } | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // Narrow screens scroll the year sideways: start at the latest weeks.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) el.scrollLeft = el.scrollWidth;
  }, []);
  const total = daily.reduce((n, d) => n + d.count, 0);
  return (
    <>
      <div class="Heatmap-scroll custom-scroll" ref={scroller}>
        <div class="Heatmap">
          {grid.map((week, w) => (
            <div key={w} class="Heatmap-week">
              {week.map((c, d) =>
                c ? (
                  <button
                    key={d}
                    type="button"
                    class={`Heatmap-cell${picked?.day === c.day ? ' picked' : ''}`}
                    data-day={c.day}
                    data-level={level(c.count)}
                    title={`${c.day} · ${c.count} 条`}
                    aria-label={`${c.day} · ${c.count} 条`}
                    onClick={() => setPicked(c)}
                  />
                ) : (
                  <span key={d} class="Heatmap-cell empty" />
                ),
              )}
            </div>
          ))}
        </div>
      </div>
      <div class="Heatmap-footer">
        <span>{picked ? `${picked.day} · ${picked.count} 条` : `最近一年 ${num(total)} 条`}</span>
        <span class="Heatmap-legend">
          少
          {[0, 1, 2, 3, 4].map((l) => (
            <span key={l} class="Heatmap-cell" data-level={l} />
          ))}
          多
        </span>
      </div>
    </>
  );
}

const W = 640;
const H = 200;
const PAD = 8;

/** Cumulative messages (line) over cumulative media bytes (area), by month; by day (messages
 * only) while the archive spans a single month, where a monthly curve would be one point. */
function Growth({ stats }: { stats: Stats }) {
  const today = localToday();
  const monthly = useMemo(() => cumulative(stats.monthly), [stats.monthly]);
  const daily = useMemo(() => dailyCumulative(stats.daily, today, stats.totals.messages), [stats.daily, today, stats.totals.messages]);
  const byMonth = monthly.length >= 2;
  const points = byMonth
    ? monthly.map((m) => ({ label: m.month, messages: m.messages, bytes: m.media_bytes }))
    : daily.map((d) => ({ label: d.day, messages: d.messages, bytes: 0 }));
  if (points.length === 0) return <p class="StatsEmpty">暂无数据</p>;
  const maxMsg = Math.max(1, ...points.map((p) => p.messages));
  const maxBytes = Math.max(1, ...points.map((p) => p.bytes));
  const x = (i: number) => (points.length === 1 ? W / 2 : PAD + (i * (W - 2 * PAD)) / (points.length - 1));
  const y = (v: number, max: number) => H - PAD - (v / max) * (H - 2 * PAD);
  const line = points.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.messages, maxMsg).toFixed(1)}`).join(' ');
  const area =
    `M${x(0).toFixed(1)},${H - PAD} ` +
    points.map((p, i) => `L${x(i).toFixed(1)},${y(p.bytes, maxBytes).toFixed(1)}`).join(' ') +
    ` L${x(points.length - 1).toFixed(1)},${H - PAD} Z`;
  const last = points[points.length - 1];
  return (
    <>
      <svg class="Growth" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label="增长曲线">
        {byMonth && <path class="Growth-area" d={area} />}
        <path class="Growth-line" d={line} vector-effect="non-scaling-stroke" />
      </svg>
      <div class="Growth-axis">
        <span>{points[0].label}</span>
        <span>{last.label}</span>
      </div>
      <div class="Growth-legend">
        <span>
          <i class="Growth-swatch line" />
          累计消息 {num(last.messages)}
        </span>
        {byMonth && (
          <span>
            <i class="Growth-swatch area" />
            累计媒体 {formatSize(last.bytes)}
          </span>
        )}
      </div>
    </>
  );
}

function Bars({
  rows,
  class: cls = '',
}: {
  class?: string;
  rows: { key: string | number; label: string; value: number; note: string; onClick?: () => void }[];
}) {
  const max = Math.max(1, ...rows.map((r) => r.value));
  return (
    <div class={`StatsBars ${cls}`}>
      {rows.map((r) => {
        const body = (
          <>
            <span class="StatsBars-label">{r.label}</span>
            <span class="StatsBars-track">
              <span class="StatsBars-fill" style={{ width: `${(r.value / max) * 100}%` }} />
            </span>
            <span class="StatsBars-note">{r.note}</span>
          </>
        );
        return r.onClick ? (
          <button key={r.key} type="button" class="StatsBars-row link" onClick={r.onClick}>
            {body}
          </button>
        ) : (
          <div key={r.key} class="StatsBars-row">
            {body}
          </div>
        );
      })}
    </div>
  );
}

function WatchRow({ w }: { w: Stats['watches'][number] }) {
  const today = localToday();
  const days = useMemo(() => {
    const by = new Map(w.daily.map((d) => [d.day, d.count]));
    return Array.from({ length: 30 }, (_, i) => {
      const day = shiftDay(today, i - 29);
      return { day, count: by.get(day) ?? 0 };
    });
  }, [w.daily, today]);
  const max = Math.max(1, ...days.map((d) => d.count));
  const rate = w.scanned > 0 ? `${(Math.min(1, w.scan_hits / w.scanned) * 100).toFixed(1).replace(/\.0$/, '')}%` : '—';
  return (
    <button type="button" class="WatchStat" disabled={!w.chat_id} onClick={() => navigate(convRoute(w.chat_id))}>
      <span class="WatchStat-head">
        <span class="WatchStat-title">{w.title || '频道'}</span>
        <span class="WatchStat-meta">{`命中 ${num(w.hits)}`}</span>
        <span class="WatchStat-meta">{`命中率 ${rate}`}</span>
      </span>
      <span class="WatchStat-bars" aria-hidden="true">
        {days.map((d) => (
          <span key={d.day} class="WatchStat-bar" title={`${d.day} · ${d.count}`} style={{ height: `${(d.count / max) * 100}%` }} />
        ))}
      </span>
    </button>
  );
}

/** Middle column: the archive in numbers. */
export function StatsView() {
  const store = useStore();
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const seq = useRef(0);

  const load = async () => {
    const my = ++seq.current;
    setLoading(true);
    setError('');
    try {
      const s = await store.api.stats(-new Date().getTimezoneOffset());
      if (my === seq.current) setStats(s);
    } catch (e) {
      if (my === seq.current) setError(errorMessage(e));
    } finally {
      if (my === seq.current) setLoading(false);
    }
  };
  useEffect(() => {
    void load();
  }, []);

  const chats = store.chats.value;
  const nameOf = (id: number) => {
    const c = chats.find((x) => x.id === id);
    return c ? chatName(c) : `会话 ${id}`;
  };

  return (
    <div id="MiddleColumn" class="ViewColumn">
      <Wallpaper />
      <ViewHeader
        icon={<ChartColumn size={22} />}
        title="统计"
        status={stats ? `${num(stats.totals.messages)} 条消息` : '加载中…'}
        actions={
          <IconButton label="刷新" class="StatsRefresh" onClick={() => void load()} disabled={loading}>
            <RefreshCw size={22} />
          </IconButton>
        }
      />
      <div class="Stats custom-scroll">
        <div class="Stats-container">
          {error && (
            <div class="StatsNotice">
              <span>{error}</span>
              <button type="button" onClick={() => void load()}>
                重试
              </button>
            </div>
          )}
          {!stats && loading && (
            <div class="StatsNotice">
              <Spinner size={32} />
            </div>
          )}
          {stats && (
            <>
              <Overview t={stats.totals} />
              <Card title="活跃度">
                <Heatmap daily={stats.daily} />
              </Card>
              <Card title="增长">
                <Growth stats={stats} />
              </Card>
              <div class="Stats-pair">
                <Card title="会话排行">
                  {stats.top_chats.length ? (
                    <Bars
                      rows={stats.top_chats.map((c) => ({
                        key: c.chat_id,
                        label: nameOf(c.chat_id),
                        value: c.messages,
                        note: `${num(c.messages)} 条 · ${formatSize(c.media_bytes)}`,
                        onClick: () => navigate(convRoute(c.chat_id)),
                      }))}
                    />
                  ) : (
                    <p class="StatsEmpty">暂无数据</p>
                  )}
                </Card>
                <Card title="媒体构成">
                  {stats.media_kinds.length ? (
                    <Bars
                      class="StatsKinds"
                      rows={[
                        ...stats.media_kinds.filter((k) => k.kind !== 'other'),
                        ...stats.media_kinds.filter((k) => k.kind === 'other'),
                      ].map((k) => ({
                        key: k.kind,
                        label: KIND_LABELS[k.kind] ?? k.kind,
                        value: k.bytes,
                        note: `${num(k.count)} · ${formatSize(k.bytes)}`,
                      }))}
                    />
                  ) : (
                    <p class="StatsEmpty">暂无数据</p>
                  )}
                  <div class="MediaStates">
                    <span class="MediaState">
                      <b>{num(stats.media_states.done)}</b>完成
                    </span>
                    <span class="MediaState">
                      <b>{num(stats.media_states.pending)}</b>下载中
                    </span>
                    <button type="button" class="MediaState link" onClick={() => navigate({ name: 'downloads' })}>
                      <b>{num(stats.media_states.failed)}</b>失败
                    </button>
                    <span class="MediaState">
                      <b>{num(stats.media_states.too_large)}</b>过大
                    </span>
                  </div>
                </Card>
              </div>
              {stats.watches.length > 0 && (
                <Card title="频道监听" class="StatsWatches">
                  <p class="StatsCard-hint">近 30 天每日命中；命中率自 v0.8.0 起统计</p>
                  {stats.watches.map((w) => (
                    <WatchRow key={w.watch_id} w={w} />
                  ))}
                </Card>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
