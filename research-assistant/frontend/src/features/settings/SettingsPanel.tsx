import { useState } from 'react';
import { Bot, Plug, Settings2 } from 'lucide-react';
import { ModelSettingsPanel } from './ModelSettingsPanel';
import { ToolsSettingsPanel } from './ToolsSettingsPanel';
import { AgentSettingsPanel } from './AgentSettingsPanel';

export type SettingsTab = 'models' | 'tools' | 'agents';

interface SettingsPanelProps {
  onClose?: () => void;
  initialTab?: SettingsTab;
}

const tabs: { id: SettingsTab; label: string; icon: typeof Settings2 }[] = [
  { id: 'models', label: '模型', icon: Settings2 },
  { id: 'tools', label: '工具', icon: Plug },
  { id: 'agents', label: 'Agent', icon: Bot },
];

/** Settings shell: model, tool and agent configuration in one place. */
export function SettingsPanel({ onClose, initialTab = 'models' }: SettingsPanelProps) {
  const [tab, setTab] = useState<SettingsTab>(initialTab);
  return (
    <div className="relative flex h-full flex-col overflow-hidden bg-paper">
      <nav className="flex shrink-0 items-center gap-1 border-b border-line bg-surface px-3 py-1.5" aria-label="设置分类">
        {tabs.map((entry) => {
          const Icon = entry.icon;
          const active = entry.id === tab;
          return (
            <button
              key={entry.id}
              type="button"
              onClick={() => setTab(entry.id)}
              aria-pressed={active}
              className={active
                ? 'inline-flex h-7 items-center gap-1 border border-ink bg-ink px-2.5 text-[12px] font-semibold text-paper'
                : 'inline-flex h-7 items-center gap-1 border border-transparent px-2.5 text-[12px] text-ink-soft hover:border-line-strong hover:text-ink'}
            >
              <Icon className="h-3.5 w-3.5" />
              {entry.label}
            </button>
          );
        })}
      </nav>
      <div className="min-h-0 flex-1">
        {tab === 'models' && <ModelSettingsPanel onClose={onClose} />}
        {tab === 'tools' && <ToolsSettingsPanel onClose={onClose} />}
        {tab === 'agents' && <AgentSettingsPanel onClose={onClose} />}
      </div>
    </div>
  );
}

export default SettingsPanel;
