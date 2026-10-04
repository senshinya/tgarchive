import { MapPin } from 'lucide-preact';
import type { Message } from '../../api/types';
import { Avatar } from '../../ui/Avatar';
import { extraNumber, extraString } from './util';
import './media.scss';

/** Location / venue: pin card linking to OpenStreetMap (no third-party tiles are loaded). */
export function Location({ msg }: { msg: Message }) {
  const lat = extraNumber(msg, 'latitude');
  const lon = extraNumber(msg, 'longitude');
  const title = extraString(msg, 'title');
  const address = extraString(msg, 'address');
  const href = `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}#map=16/${lat}/${lon}`;
  return (
    <a class="Location" href={href} target="_blank" rel="noopener noreferrer">
      <div class="Location-map">
        <MapPin size={40} class="Location-pin" />
      </div>
      <div class="Location-info">
        <div class="Location-title">{title || '位置'}</div>
        <div class="Location-subtitle">{address || `${lat.toFixed(6)}, ${lon.toFixed(6)}`}</div>
      </div>
    </a>
  );
}

export function Contact({ msg }: { msg: Message }) {
  const name = `${extraString(msg, 'first_name')} ${extraString(msg, 'last_name')}`.trim() || '联系人';
  const phone = extraString(msg, 'phone_number');
  const uid = extraNumber(msg, 'user_id');
  return (
    <div class="Contact">
      <Avatar name={name} peerId={uid || phone.length} size="large" />
      <div class="Contact-info">
        <div class="Contact-name">{name}</div>
        {phone && <div class="Contact-phone">{phone}</div>}
      </div>
    </div>
  );
}

interface PollOption {
  text: string;
  voter_count: number;
}

export function Poll({ msg }: { msg: Message }) {
  const question = extraString(msg, 'question');
  const options = (Array.isArray(msg.extra?.options) ? msg.extra!.options : []) as PollOption[];
  const total = extraNumber(msg, 'total_voter_count');
  const quiz = extraString(msg, 'poll_type') === 'quiz';
  const anonymous = msg.extra?.is_anonymous !== false;
  const multiple = msg.extra?.multiple === true;
  const kind = `${anonymous ? '匿名' : '公开'}${quiz ? '测验' : '投票'}${multiple ? ' · 多选' : ''}`;
  return (
    <div class="Poll">
      <div class="Poll-question">{question}</div>
      <div class="Poll-type">{kind}</div>
      <div class="Poll-options">
        {options.map((o, i) => {
          const pct = total > 0 ? Math.round((o.voter_count / total) * 100) : 0;
          return (
            <div class="Poll-option" key={i}>
              <span class="Poll-percent">{pct}%</span>
              <div class="Poll-option-body">
                <div class="Poll-option-text">{o.text}</div>
                <div class="Poll-bar" style={{ width: `${Math.max(pct, 1)}%` }} />
              </div>
            </div>
          );
        })}
      </div>
      <div class="Poll-total">{total > 0 ? `${total} 人投票` : '暂无投票'}</div>
    </div>
  );
}

export function Dice({ msg }: { msg: Message }) {
  const emoji = extraString(msg, 'emoji') || '🎲';
  const value = extraNumber(msg, 'value');
  return (
    <div class="Dice" title={`点数：${value}`}>
      <span class="Dice-emoji">{emoji}</span>
      <span class="Dice-value">点数：{value}</span>
    </div>
  );
}
