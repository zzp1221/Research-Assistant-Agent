import { useId, useRef } from 'react';
import { ChevronDown, ChevronUp, Eye, FileText, HelpCircle, Loader2, Paperclip, Send, ShieldCheck, X } from 'lucide-react';
import { useAutoRunOnCreateControl } from '../../app/hooks/useResearchAssistantChatFlow';
import { uiText } from '../../app/constants/uiText';
import type { UploadedFile } from '../../contracts/api';
import { isPdfAttachment } from '../pdf-viewer/pdfAttachment';

interface ChatComposerProps {
  prompt: string;
  hasConversation?: boolean;
  loading: boolean;
  isLoggedIn: boolean;
  showSuggestions: boolean;
  pendingAttachments: UploadedFile[];
  uploadingAttachments: boolean;
  attachmentError: string;
  setPrompt: (value: string) => void;
  setShowSuggestions: (next: boolean) => void;
  onSendMessage: () => void;
  onAttachFiles: (files: File[]) => void;
  onRemoveAttachment: (uploadId: string) => void;
  onOpenAttachment: (attachment: UploadedFile) => void;
  runtimePermissionMode?: 'confirm_important' | 'full_access';
  onRuntimePermissionModeChange?: (mode: 'confirm_important' | 'full_access') => void;
}

const suggestions = [
  '帮我画一个正弦函数和余弦函数的对比图',
  '复现一下上传论文的核心方法并跑通最小测试',
  '对比一下 LangChain 和 LlamaIndex 的 RAG 性能',
  '分析一下这篇论文的主要创新点和局限性',
  '帮我复现上传论文中的核心实验',
];

/** The instruction block: what the author wants set, before it is typeset. */
export function ChatComposer(props: ChatComposerProps) {
  const {
    prompt, hasConversation = false, loading, isLoggedIn, showSuggestions, pendingAttachments, uploadingAttachments, attachmentError,
    setPrompt, setShowSuggestions, onSendMessage, onAttachFiles, onRemoveAttachment, onOpenAttachment,
    runtimePermissionMode = 'confirm_important', onRuntimePermissionModeChange = () => undefined,
  } = props;
  const fileInputRef = useRef<HTMLInputElement>(null);
  const autoRunCopyId = useId();
  const { autoRunOnCreate, setAutoRunOnCreate } = useAutoRunOnCreateControl();

  return (
    <div className="shrink-0 border-t border-line bg-surface px-4 pb-3 pt-2.5">
      {!hasConversation ? (
        <div className="mb-1.5 flex items-baseline justify-between">
          <button
            type="button"
            onClick={() => setShowSuggestions(!showSuggestions)}
            className="flex items-center gap-1 journal-caps text-[10px] transition-colors hover:text-ink"
            aria-expanded={showSuggestions}
          >
            起始选题
            {showSuggestions ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
          </button>
          <span className="journal-caps text-[10px]">PDF · 数据 · 代码</span>
        </div>
      ) : (
        <div className="mb-1.5 flex items-baseline justify-between">
          <span className="journal-caps text-[10px]">投稿说明</span>
          <span className="journal-caps text-[10px]">PDF · 数据 · 代码</span>
        </div>
      )}

      {!hasConversation && showSuggestions && (
        <ol className="proof-fade mb-2.5 border-t border-line">
          {suggestions.map((text, index) => (
            <li key={text} className="border-b border-line">
              <button
                type="button"
                onClick={() => setPrompt(text)}
                className="group grid w-full grid-cols-[1.6rem_minmax(0,1fr)] items-baseline gap-2 py-1.5 text-left"
              >
                <span className="journal-num text-[10.5px] text-muted">{String(index + 1).padStart(2, '0')}</span>
                <span className="font-serif text-[12.5px] leading-5 text-ink-soft transition-colors group-hover:text-ink">{text}</span>
              </button>
            </li>
          ))}
        </ol>
      )}

      {(pendingAttachments.length > 0 || uploadingAttachments || attachmentError) && (
        <div className="mb-2 flex flex-wrap gap-1.5" aria-live="polite">
          {pendingAttachments.map((attachment) => (
            <div key={attachment.id} className="flex max-w-full items-center gap-1.5 border border-line bg-surface-muted px-2 py-1 text-[11.5px] text-ink-soft">
              <FileText className="h-3.5 w-3.5 shrink-0" />
              <span className="max-w-44 truncate font-serif">{attachment.name}</span>
              <span className="journal-num shrink-0 text-[10.5px] text-muted">{Math.max(1, Math.round(attachment.size / 1024))} KB</span>
              {isPdfAttachment(attachment) && (
                <button
                  type="button"
                  onClick={() => onOpenAttachment(attachment)}
                  className="p-0.5 text-muted transition-colors hover:bg-surface-strong hover:text-ink"
                  title="在阅读器中打开 PDF"
                  aria-label={`打开 ${attachment.name}`}
                >
                  <Eye className="h-3.5 w-3.5" />
                </button>
              )}
              <button
                type="button"
                onClick={() => onRemoveAttachment(attachment.id)}
                className="p-0.5 text-muted transition-colors hover:bg-proof-soft hover:text-proof"
                title="移除附件"
                aria-label={`移除 ${attachment.name}`}
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
          {uploadingAttachments && (
            <span className="flex items-center gap-1.5 border border-line bg-surface-muted px-2 py-1 text-[11.5px] text-ink-soft">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              正在收稿
            </span>
          )}
          {attachmentError && <span className="w-full text-[11.5px] text-proof">{attachmentError}</span>}
        </div>
      )}

      <div className="border border-ink bg-surface transition-shadow focus-within:shadow-[0_0_0_1px_#17130f]">
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              if (!loading && !e.nativeEvent.isComposing) {
                onSendMessage();
              }
            }
          }}
          placeholder={
            isLoggedIn
              ? '写下研究目标，或上传论文让助手复现实验…'
              : '游客模式下也可直接提问；登录后按稿次持久保存'
          }
          aria-label="消息输入框"
          className="block min-h-[76px] w-full resize-none bg-transparent px-3 pb-2 pt-2.5 font-serif text-[13.5px] leading-6 text-ink outline-none placeholder:text-muted"
          rows={3}
        />
        <div className="flex items-center justify-between gap-2 border-t border-line bg-surface-muted px-2 py-1.5">
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              disabled={loading || uploadingAttachments || pendingAttachments.length >= 8}
              className="flex h-7 w-7 items-center justify-center text-ink-soft transition-colors hover:bg-surface-strong disabled:cursor-not-allowed disabled:opacity-40"
              title="添加论文、数据或实验配置"
              aria-label="添加附件"
            >
              {uploadingAttachments ? <Loader2 className="h-4 w-4 animate-spin" /> : <Paperclip className="h-4 w-4" />}
            </button>
            <span className="hidden journal-caps text-[10px] sm:inline">Enter 提交 · Shift+Enter 换行</span>
          </div>
          <button
            type="button"
            onClick={onSendMessage}
            disabled={loading || uploadingAttachments || (!prompt.trim() && pendingAttachments.length === 0)}
            className="flex h-7 items-center gap-1.5 bg-ink px-3 text-[12px] font-semibold text-paper transition-colors hover:bg-black active:translate-y-px disabled:cursor-not-allowed disabled:bg-[#e6e0d3] disabled:text-[#4a4238]"
            title="发送消息"
            aria-label="发送消息"
          >
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3 w-3" />}
            发送
          </button>
        </div>
      </div>

      <div className="mt-2 flex items-center justify-between gap-3">
        <div className="min-w-0">
          <span id={`${autoRunCopyId}-label`} className="journal-caps text-[10px]">
            {uiText.autoRunSwitchLabel}
          </span>
          <p id={`${autoRunCopyId}-hint`} className="mt-0.5 font-serif text-[11.5px] leading-4 text-muted">
            {uiText.autoRunSwitchHint}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className={`journal-caps text-[10px] ${autoRunOnCreate ? 'text-ink' : 'text-muted'}`} aria-hidden="true">
            {autoRunOnCreate ? '自动' : '手动'}
          </span>
          <button
            type="button"
            role="switch"
            aria-checked={autoRunOnCreate}
            aria-labelledby={`${autoRunCopyId}-label`}
            aria-describedby={`${autoRunCopyId}-hint`}
            onClick={() => setAutoRunOnCreate(!autoRunOnCreate)}
            className={`flex h-5 w-9 shrink-0 items-center border p-0.5 transition-colors ${autoRunOnCreate ? 'justify-end border-ink bg-ink' : 'justify-start border-line-strong bg-surface hover:border-ink'}`}
          >
            <span className={`block h-3.5 w-3.5 transition-colors ${autoRunOnCreate ? 'bg-paper' : 'bg-ink-soft'}`} aria-hidden="true" />
          </button>
        </div>
      </div>

      <div className="mt-2 flex items-start justify-between gap-3 border-t border-line pt-2">
        <div className="flex items-center gap-1.5">
          <ShieldCheck className="h-3.5 w-3.5 text-muted" />
          <span className="journal-caps text-[10px]">运行权限</span>
          <button
            type="button"
            className="flex h-4 w-4 items-center justify-center text-muted hover:text-ink"
            aria-label="查看运行权限说明"
            title={runtimePermissionMode === 'full_access'
              ? '完全权限：自动拉镜像、装依赖、下载数据与 GPU 操作；仍受镜像白名单和资源上限约束。'
              : '确认模式：拉取镜像、安装依赖、下载数据集/模型、使用 GPU 前会暂停并请求你的确认。'}
          >
            <HelpCircle className="h-3 w-3" />
          </button>
        </div>
        <div className="flex items-center border border-line-strong" role="group" aria-label="运行权限">
          <button
            type="button"
            onClick={() => onRuntimePermissionModeChange('confirm_important')}
            className={`h-6 px-2 text-[11px] font-semibold transition-colors ${runtimePermissionMode === 'confirm_important' ? 'bg-ink text-paper' : 'bg-surface text-ink-soft hover:bg-surface-muted'}`}
            aria-pressed={runtimePermissionMode === 'confirm_important'}
          >
            确认模式
          </button>
          <button
            type="button"
            onClick={() => onRuntimePermissionModeChange('full_access')}
            className={`h-6 border-l border-line-strong px-2 text-[11px] font-semibold transition-colors ${runtimePermissionMode === 'full_access' ? 'bg-proof text-white' : 'bg-surface text-ink-soft hover:bg-surface-muted'}`}
            aria-pressed={runtimePermissionMode === 'full_access'}
          >
            完全权限
          </button>
        </div>
      </div>

      <p className="mt-1.5 font-serif text-[11.5px] leading-4 text-muted">
        {runtimePermissionMode === 'full_access'
          ? '自动执行镜像、依赖、数据下载与 GPU 操作，仍受白名单与资源上限约束。'
          : '镜像、依赖、数据下载与 GPU 操作前暂停，逐项确认。'}
      </p>

      <input
        ref={fileInputRef}
        type="file"
        multiple
        accept=".pdf,.txt,.md,.json,.jsonl,.yaml,.yml,.toml,.py,.ipynb,.csv,.tsv"
        className="hidden"
        onChange={(event) => {
          onAttachFiles(Array.from(event.target.files ?? []));
          event.target.value = '';
        }}
      />
    </div>
  );
}