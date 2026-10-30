import {
  BookOpen,
  CircleHelp,
  Inbox,
  Library,
  MessageSquareText,
  Settings,
  ShieldCheck,
  Wallet,
} from 'lucide-react';
import type { ReactNode } from 'react';

interface WorkbenchRailProps {
  activeView: 'chat' | 'literature';
  planId: string | null;
  pendingWorkCount: number;
  reviewPending: boolean;
  accountLabel: string;
  onSelectView: (view: 'chat' | 'literature') => void;
  onOpenPlanLibrary: () => void;
  onOpenCostConsole: () => void;
  onOpenInbox: () => void;
  onOpenReview: () => void;
  onOpenHelp: () => void;
  onOpenModelSettings?: () => void;
}

interface RailButtonProps {
  icon: ReactNode;
  label: string;
  active?: boolean;
  badge?: number;
  dot?: boolean;
  disabled?: boolean;
  onClick: () => void;
}

function RailButton({ icon, label, active = false, badge, dot, disabled, onClick }: RailButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      aria-pressed={active || undefined}
      className={`research-assistant-rail-btn ${active ? 'is-active' : ''}`}
    >
      {icon}
      {typeof badge === 'number' && badge > 0 ? <span className="research-assistant-rail-badge">{badge}</span> : null}
      {dot ? <span className="research-assistant-rail-dot" aria-hidden="true" /> : null}
    </button>
  );
}

/**
 * The workbench rail keeps navigation and secondary tools out of the canvas.
 * The top group is content navigation; the bottom group is operational.
 */
export function WorkbenchRail(props: WorkbenchRailProps) {
  const {
    activeView,
    planId,
    pendingWorkCount,
    reviewPending,
    accountLabel,
    onSelectView,
    onOpenPlanLibrary,
    onOpenCostConsole,
    onOpenInbox,
    onOpenReview,
    onOpenHelp,
    onOpenModelSettings,
  } = props;

  return (
    <aside className="research-assistant-rail" aria-label="工作台导航">
      <div className="research-assistant-rail-brand" title="Research Assistant">R</div>
      <nav className="research-assistant-rail-group" aria-label="内容">
        <RailButton
          icon={<MessageSquareText className="h-[18px] w-[18px]" />}
          label="对话"
          active={activeView === 'chat'}
          onClick={() => onSelectView('chat')}
        />
        <RailButton
          icon={<BookOpen className="h-[18px] w-[18px]" />}
          label="文献库"
          active={activeView === 'literature'}
          onClick={() => onSelectView('literature')}
        />
        <RailButton
          icon={<Library className="h-[18px] w-[18px]" />}
          label="计划库"
          onClick={onOpenPlanLibrary}
        />
      </nav>

      <div className="research-assistant-rail-spacer" />

      <div className="research-assistant-rail-group" aria-label="工具">
        <RailButton
          icon={<Wallet className="h-[18px] w-[18px]" />}
          label="成本与预算"
          onClick={onOpenCostConsole}
        />
        <RailButton
          icon={<Inbox className="h-[18px] w-[18px]" />}
          label="待办中心"
          badge={pendingWorkCount}
          onClick={onOpenInbox}
        />
        <RailButton
          icon={<ShieldCheck className="h-[18px] w-[18px]" />}
          label="计划审阅"
          dot={reviewPending}
          disabled={!planId}
          onClick={onOpenReview}
        />
        <RailButton
          icon={<CircleHelp className="h-[18px] w-[18px]" />}
          label="使用手册"
          onClick={onOpenHelp}
        />
        {onOpenModelSettings ? (
          <RailButton
            icon={<Settings className="h-[18px] w-[18px]" />}
            label="模型设置"
            onClick={onOpenModelSettings}
          />
        ) : null}
      </div>

      <span className="research-assistant-rail-avatar" title={accountLabel}>
        {accountLabel.startsWith('账户') ? '研' : '游'}
      </span>
    </aside>
  );
}
