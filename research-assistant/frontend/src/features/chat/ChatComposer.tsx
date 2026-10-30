import { useId, useRef } from 'react';
import { BookOpen, ChevronDown, Eye, FileText, HelpCircle, Loader2, Paperclip, Send, ShieldCheck, X } from 'lucide-react';
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
  onOpenLiterature?: () => void;
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

/** A quiet command surface: one text area, one primary send, tools on demand. */
export function ChatComposer(props: ChatComposerProps) {
  const {
    prompt, hasConversation = false, loading, isLoggedIn, showSuggestions, pendingAttachments, uploadingAttachments, attachmentError,
    setPrompt, setShowSuggestions, onSendMessage, onAttachFiles, onRemoveAttachment, onOpenAttachment, onOpenLiterature,
    runtimePermissionMode = 'confirm_important', onRuntimePermissionModeChange = () => undefined,
  } = props;
  const fileInputRef = useRef<HTMLInputElement>(null);
  const autoRunCopyId = useId();
  const { autoRunOnCreate, setAutoRunOnCreate } = useAutoRunOnCreateControl();

  return (
    <div className="shrink-0 border-t border-line bg-surface px-4 pb-3.5 pt-3">
      {!hasConversation && showSuggestions ? (
        <div className="proof-fade mb-2.5 flex flex-wrap gap-1.5">
          {suggestions.slice(0, 3).map((text) => (
            <button
              key={text}
              type="button"
              onClick={() => setPrompt(text)}
              className="rounded-full border border-line bg-surface-muted px-2.5 py-1.5 text-left text-[10.5px] leading-4 text-ink-soft transition-colors hover:border-accent/40 hover:bg-accent-soft hover:text-accent"
            >
              {text}
            </button>
          ))}
          <button
            type="button"
            onClick={() => setShowSuggestions(false)}
            className="rounded-full px-2 py-1.5 text-[10px] text-muted transition-colors hover:text-ink"
          >
            收起
          </button>
        </div>
      ) : null}

      <div className="rounded-2xl border border-line-strong bg-surface-muted p-1.5 transition-colors focus-within:border-accent focus-within:bg-surface focus-within:shadow-[0_0_0_3px_rgba(31,107,79,0.08)]">
        {(pendingAttachments.length > 0 || uploadingAttachments || attachmentError) ? (
          <div className="mb-1 flex flex-wrap gap-1.5 px-1 pt-1" aria-live="polite">
            {pendingAttachments.map((attachment) => (
              <div key={attachment.id} className="flex max-w-full items-center gap-1.5 rounded-lg border border-line bg-surface px-2 py-1 text-[10.5px] text-ink-soft">
                <FileText className="h-3.5 w-3.5 shrink-0" />
                <span className="max-w-44 truncate">{attachment.name}</span>
                <span className="shrink-0 font-mono text-[9.5px] text-muted">{Math.max(1, Math.round(attachment.size / 1024))} KB</span>
                {isPdfAttachment(attachment) ? (
                  <button
                    type="button"
                    onClick={() => onOpenAttachment(attachment)}
                    className="rounded p-0.5 text-muted transition-colors hover:bg-surface-muted hover:text-ink"
                    title="在阅读器中打开 PDF"
                    aria-label={`打开 ${attachment.name}`}
                  >
                    <Eye className="h-3.5 w-3.5" />
                  </button>
                ) : null}
                <button
                  type="button"
                  onClick={() => onRemoveAttachment(attachment.id)}
                  className="rounded p-0.5 text-muted transition-colors hover:bg-proof-soft hover:text-proof"
                  title="移除附件"
                  aria-label={`移除 ${attachment.name}`}
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
            {uploadingAttachments ? (
              <span className="flex items-center gap-1.5 rounded-lg border border-line bg-surface px-2 py-1 text-[10.5px] text-ink-soft">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                正在上传
              </span>
            ) : null}
            {attachmentError ? <span className="w-full text-[10.5px] text-proof">{attachmentError}</span> : null}
          </div>
        ) : null}

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
              : '游客模式下可直接提问；会话保存在本机，登录后可跨设备恢复'
          }
          aria-label="消息输入框"
          className="block min-h-[56px] w-full resize-none bg-transparent px-2.5 py-2 text-[13px] leading-[1.6] text-ink outline-none placeholder:text-muted"
          rows={2}
        />
        <div className="flex items-center justify-between gap-2 px-1 pb-0.5">
          <div className="flex items-center gap-0.5">
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              disabled={loading || uploadingAttachments || pendingAttachments.length >= 8}
              className="flex h-7 w-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-strong hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
              title="添加论文、数据或实验配置"
              aria-label="添加附件"
            >
              {uploadingAttachments ? <Loader2 className="h-4 w-4 animate-spin" /> : <Paperclip className="h-4 w-4" />}
            </button>
            {onOpenLiterature ? (
              <button
                type="button"
                onClick={onOpenLiterature}
                className="flex h-7 items-center gap-1 rounded-lg px-1.5 text-[10px] text-muted transition-colors hover:bg-surface-strong hover:text-ink"
                title="从文献库选择论文并加入当前会话"
                aria-label="从文献库添加"
              >
                <BookOpen className="h-3.5 w-3.5" />
                <span className="hidden sm:inline">文献库</span>
              </button>
            ) : null}
            <button
              type="button"
              onClick={() => setShowSuggestions(!showSuggestions)}
              aria-expanded={showSuggestions}
              className="hidden h-7 items-center rounded-lg px-1.5 text-[10px] text-muted transition-colors hover:bg-surface-strong hover:text-ink sm:flex"
              title={showSuggestions ? '收起示例任务' : '展开示例任务'}
            >
              示例
            </button>
          </div>
          <button
            type="button"
            onClick={onSendMessage}
            disabled={loading || uploadingAttachments || (!prompt.trim() && pendingAttachments.length === 0)}
            className="flex h-8 items-center gap-1.5 rounded-xl bg-accent px-3 text-[11px] font-semibold text-white shadow-[0_10px_18px_-14px_rgba(31,107,79,0.9)] transition-colors hover:bg-accent-strong active:translate-y-px disabled:cursor-not-allowed disabled:bg-surface-strong disabled:text-muted disabled:shadow-none"
            title="发送消息"
            aria-label="发送消息"
          >
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3 w-3" />}
            发送
          </button>
        </div>
      </div>

      <div className="mt-2 flex items-center justify-between gap-3 px-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <button
            id={`${autoRunCopyId}-switch`}
            type="button"
            role="switch"
            aria-checked={autoRunOnCreate}
            aria-labelledby={`${autoRunCopyId}-label`}
            aria-describedby={`${autoRunCopyId}-hint`}
            onClick={() => setAutoRunOnCreate(!autoRunOnCreate)}
            className="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full bg-surface-strong p-0.5 transition-colors aria-[checked=true]:bg-accent"
          >
            <span className={`block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${autoRunOnCreate ? 'translate-x-4' : ''}`} />
          </button>
          <span className="min-w-0">
            <span id={`${autoRunCopyId}-label`} className="block truncate text-[10.5px] font-medium text-ink-soft">{uiText.autoRunSwitchLabel}</span>
            <span id={`${autoRunCopyId}-hint`} className="mt-0.5 block text-[9.5px] leading-4 text-muted">{uiText.autoRunSwitchHint}</span>
          </span>
        </div>

        <details className="group relative shrink-0">
          <summary className="flex cursor-pointer list-none items-center gap-1.5 rounded-full border border-line bg-surface px-2.5 py-1 text-[10px] text-muted [&::-webkit-details-marker]:hidden hover:border-line-strong hover:text-ink-soft">
            <ShieldCheck className="h-3 w-3" />
            {runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}
            <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
          </summary>
          <div className="absolute bottom-[calc(100%+8px)] right-0 z-30 w-56 rounded-xl border border-line bg-surface p-2.5 shadow-float">
            <div className="flex items-center gap-1.5 text-[10px] font-semibold text-ink">
              高风险操作
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
            <div className="mt-2 flex items-center rounded-lg bg-surface-muted p-0.5" role="group" aria-label="运行权限">
              <button
                type="button"
                onClick={() => onRuntimePermissionModeChange('confirm_important')}
                className={`h-6 flex-1 rounded-md px-2 text-[10px] font-semibold transition-colors ${runtimePermissionMode === 'confirm_important' ? 'bg-white text-ink shadow-sm' : 'text-muted hover:text-ink'}`}
                aria-pressed={runtimePermissionMode === 'confirm_important'}
              >
                确认
              </button>
              <button
                type="button"
                onClick={() => onRuntimePermissionModeChange('full_access')}
                className={`h-6 flex-1 rounded-md px-2 text-[10px] font-semibold transition-colors ${runtimePermissionMode === 'full_access' ? 'bg-proof text-white shadow-sm' : 'text-muted hover:text-ink'}`}
                aria-pressed={runtimePermissionMode === 'full_access'}
              >
                完全
              </button>
            </div>
            <p className="mt-2 text-[9.5px] leading-4 text-muted">
              {runtimePermissionMode === 'full_access'
                ? '自动执行镜像、依赖、数据下载与 GPU 操作，仍受白名单与资源上限约束。'
                : '镜像、依赖、数据下载与 GPU 操作前暂停，逐项确认。'}
            </p>
          </div>
        </details>
      </div>

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
