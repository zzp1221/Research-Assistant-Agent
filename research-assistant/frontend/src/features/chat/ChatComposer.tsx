import { useRef } from 'react';
import { ChevronDown, ChevronUp, Eye, FileText, Loader2, Paperclip, Send, Sparkles, X } from 'lucide-react';
import type { UploadedFile } from '../../contracts/api';
import { isPdfAttachment } from '../pdf-viewer/pdfAttachment';

interface ChatComposerProps {
  prompt: string;
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
}

const suggestions = [
  '帮我画一个正弦函数和余弦函数的对比图',
  '复现一下上传论文的核心方法并跑通最小测试',
  '对比一下 LangChain 和 LlamaIndex 的 RAG 性能',
  '分析一下这篇论文的主要创新点和局限性',
  '帮我复现上传论文中的核心实验',
];

export function ChatComposer(props: ChatComposerProps) {
  const {
    prompt, loading, isLoggedIn, showSuggestions, pendingAttachments, uploadingAttachments, attachmentError,
    setPrompt, setShowSuggestions, onSendMessage, onAttachFiles, onRemoveAttachment, onOpenAttachment,
  } = props;
  const fileInputRef = useRef<HTMLInputElement>(null);

  return (
    <div className="shrink-0 border-t border-line bg-surface px-3 pb-3 pt-2">
      <div className="mb-1.5 flex items-center justify-between px-1">
        <button
          type="button"
          onClick={() => setShowSuggestions(!showSuggestions)}
          className="group flex items-center gap-1.5 rounded-[4px] py-1 text-[10.5px] font-semibold tracking-[0.04em] text-muted transition-colors hover:text-accent-ink"
          aria-expanded={showSuggestions}
        >
          <Sparkles className="h-3 w-3 text-accent/80" />
          试试推荐指令
          {showSuggestions ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
        </button>
        <span className="text-[10px] text-faint">支持 PDF、数据与代码附件</span>
      </div>

      {showSuggestions && (
        <div className="ra-fade-up mb-2 flex flex-wrap gap-1.5">
          {suggestions.map((text) => (
            <button
              key={text}
              type="button"
              onClick={() => setPrompt(text)}
              className="rounded-full border border-line bg-surface-muted px-2.5 py-1.5 text-left text-[11.5px] leading-4 text-ink-soft transition-all hover:border-accent/35 hover:bg-accent-soft hover:text-accent-ink active:scale-[0.98]"
            >
              {text}
            </button>
          ))}
        </div>
      )}

      {(pendingAttachments.length > 0 || uploadingAttachments || attachmentError) && (
        <div className="mb-2 flex flex-wrap gap-1.5" aria-live="polite">
          {pendingAttachments.map((attachment) => (
            <div key={attachment.id} className="flex max-w-full items-center gap-1.5 rounded-[5px] border border-line bg-surface-muted px-2 py-1 text-[11.5px] text-ink-soft">
              <FileText className="h-3.5 w-3.5 shrink-0 text-accent" />
              <span className="max-w-44 truncate">{attachment.name}</span>
              <span className="shrink-0 text-[10px] text-faint">{Math.max(1, Math.round(attachment.size / 1024))} KB</span>
              {isPdfAttachment(attachment) && (
                <button
                  type="button"
                  onClick={() => onOpenAttachment(attachment)}
                  className="rounded-[3px] p-0.5 text-faint transition-colors hover:bg-accent-soft hover:text-accent-ink"
                  title="在阅读器中打开 PDF"
                  aria-label={`打开 ${attachment.name}`}
                >
                  <Eye className="h-3.5 w-3.5" />
                </button>
              )}
              <button
                type="button"
                onClick={() => onRemoveAttachment(attachment.id)}
                className="rounded-[3px] p-0.5 text-faint transition-colors hover:bg-flag-soft hover:text-flag"
                title="移除附件"
                aria-label={`移除 ${attachment.name}`}
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
          {uploadingAttachments && (
            <span className="flex items-center gap-1.5 rounded-[5px] border border-line bg-surface-muted px-2 py-1 text-[11.5px] text-accent">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              正在上传
            </span>
          )}
          {attachmentError && <span className="w-full text-[11.5px] text-flag">{attachmentError}</span>}
        </div>
      )}

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

      <div className="relative rounded-[8px] border border-line bg-surface shadow-[0_1px_2px_rgb(61_52_36/0.04)] transition-[border-color,box-shadow] focus-within:border-accent/55 focus-within:shadow-[0_0_0_3px_rgb(44_90_77/0.08)]">
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
              ? '描述研究目标，或上传论文让助手复现实验…'
              : '游客模式下也可直接提问；登录后会持久保存会话记录'
          }
          aria-label="消息输入框"
          className="min-h-[82px] w-full resize-none bg-transparent px-3 pb-9 pt-2.5 text-[13.5px] leading-6 text-ink outline-none placeholder:text-faint"
          rows={3}
        />
        <div className="absolute inset-x-1.5 bottom-1.5 flex items-center justify-between gap-2">
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            disabled={loading || uploadingAttachments || pendingAttachments.length >= 8}
            className="flex h-7 w-7 items-center justify-center rounded-[4px] text-muted transition-colors hover:bg-surface-muted hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
            title="添加论文、数据或实验配置"
            aria-label="添加附件"
          >
            {uploadingAttachments ? <Loader2 className="h-4 w-4 animate-spin" /> : <Paperclip className="h-4 w-4" />}
          </button>

          <div className="flex items-center gap-2">
            <span className="hidden text-[10px] text-faint sm:inline">Enter 发送 · Shift+Enter 换行</span>
            <button
              type="button"
              onClick={onSendMessage}
              disabled={loading || uploadingAttachments || (!prompt.trim() && pendingAttachments.length === 0)}
              className="flex h-7 w-7 items-center justify-center rounded-[5px] bg-accent text-white transition-all hover:bg-accent-strong active:scale-[0.94] disabled:cursor-not-allowed disabled:bg-line disabled:text-faint"
              title="发送消息"
              aria-label="发送消息"
            >
              {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
