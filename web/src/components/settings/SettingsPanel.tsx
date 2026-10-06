import type { Route } from '../../lib/router';
import { AddBot } from './AddBot';
import { BotSettings } from './BotSettings';
import { SettingsHome } from './SettingsHome';
import { TelegramAppSettings } from './TelegramAppSettings';
import { UserbotSettings } from './UserbotSettings';
import { WatchEditor } from '../watch/WatchEditor';
import { WatchList } from '../watch/WatchList';

/** Admin pages rendered inside the left column, like Web A's settings. */
export function SettingsPanel({ route }: { route: Route }) {
  switch (route.name) {
    case 'settings-add-bot':
      return <AddBot />;
    case 'settings-bot':
      return <BotSettings key={route.botId} botId={route.botId} />;
    case 'settings-telegram-app':
      return <TelegramAppSettings />;
    case 'settings-userbot':
      return <UserbotSettings />;
    case 'settings-watches':
      return <WatchList />;
    case 'settings-watch-new':
      return <WatchEditor key="new" />;
    case 'settings-watch':
      return <WatchEditor key={route.watchId} watchId={route.watchId} />;
    default:
      return <SettingsHome />;
  }
}
