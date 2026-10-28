import { useEffect, useRef, useState } from 'react';
import {
  ArrowRight,
  BookOpen,
  FileSearch,
  FileText,
  GitBranch,
  Highlighter,
  Languages,
  Library,
  ListChecks,
  Play,
  Search,
  X,
} from 'lucide-react';

export type HelpSection = 'start' | 'library' | 'pdf' | 'graph';

const sections: Array<{ id: HelpSection; label: string; icon: typeof BookOpen }> = [
  { id: 'start', label: '快速开始', icon: BookOpen },
  { id: 'library', label: '文献库', icon: Library },
  { id: 'pdf', label: 'PDF 阅读', icon: FileText },
  { id: 'graph', label: '执行图', icon: GitBranch },
];

interface HelpDrawerProps {
  onClose: () => void;
  initialSection?: HelpSection;
}

function Step(props: { n: string; title: string; children: React.ReactNode }) {
  return (
    <li className="grid grid-cols-[24px_minmax(0,1fr)] gap-3 border-b border-line/70 py-3 last:border-b-0">
      <span className="flex h-6 w-6 items-center justify-center border border-ink font-mono text-[10px] font-semibold">{props.n}</span>
      <span className="min-w-0">
        <b className="block font-serif text-[13px] font-semibold leading-5 text-ink">{props.title}</b>
        <span className="mt-0.5 block text-[11.5px] leading-5 text-ink-soft">{props.children}</span>
      </span>
    </li>
  );
}

function Feature(props: { icon: React.ReactNode; title: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-2.5 border border-line bg-surface px-3 py-2.5">
      <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center border border-line-strong text-ink-soft">{props.icon}</span>
      <span className="min-w-0">
        <b className="block text-[12px] font-semibold leading-4 text-ink">{props.title}</b>
        <span className="mt-0.5 block text-[11px] leading-5 text-muted">{props.children}</span>
      </span>
    </div>
  );
}

export function HelpDrawer({ onClose, initialSection = 'start' }: HelpDrawerProps) {
  const [section, setSection] = useState<HelpSection>(initialSection);
  const closeRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    closeRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      previous?.focus?.();
    };
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-[80] flex justify-end" role="presentation">
      <div className="absolute inset-0 bg-ink/30" onClick={onClose} aria-hidden="true" />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="help-drawer-title"
        className="proof-slide relative flex h-full w-full max-w-[460px] flex-col border-l border-ink bg-paper shadow-float"
      >
        <header className="flex shrink-0 items-start justify-between gap-4 border-b-2 border-ink bg-surface px-5 py-4">
          <div>
            <div className="journal-caps text-[10px]">Research Assistant · 使用手册</div>
            <h2 id="help-drawer-title" className="mt-1 font-serif text-[21px] font-semibold leading-7 text-ink">2 分钟上手</h2>
            <p className="mt-1 text-[11.5px] leading-5 text-muted">文献、阅读、计划与执行，都在同一个工作台里。</p>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            className="flex h-8 w-8 shrink-0 items-center justify-center border border-transparent text-muted transition-colors hover:border-line-strong hover:bg-surface-muted hover:text-ink"
            aria-label="关闭使用手册"
          >
            <X className="h-4 w-4" />
          </button>
        </header>

        <nav className="flex shrink-0 border-b border-line bg-surface px-3" aria-label="手册章节">
          {sections.map((item) => {
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => setSection(item.id)}
                aria-pressed={section === item.id}
                className={`flex h-10 flex-1 items-center justify-center gap-1.5 border-b-2 px-1 text-[11.5px] transition-colors ${
                  section === item.id
                    ? 'border-ink font-semibold text-ink'
                    : 'border-transparent text-muted hover:text-ink-soft'
                }`}
              >
                <Icon className="h-3.5 w-3.5" />
                {item.label}
              </button>
            );
          })}
        </nav>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {section === 'start' ? (
            <div>
              <p className="font-serif text-[13px] leading-6 text-ink-soft">
                你可以把这里当成一张研究工作台：左边对话与文献，中间是论文原文，右边是执行图与节点详情。下面是最短的上手路径。
              </p>
              <ol className="mt-3 border-t border-line">
                <Step n="01" title="把材料放进来">上传 PDF、数据或代码；也可以在「文献库」检索真实论文，导入 PDF。</Step>
                <Step n="02" title="提出研究目标">在对话里描述要做什么，例如「分析这篇论文的创新点和局限」或「复现核心实验」。</Step>
                <Step n="03" title="核对并运行执行图">系统会先生成节点图。确认节点和依赖后，在右侧启动计划，日志和产物都会保留。</Step>
              </ol>
              <div className="mt-4 grid gap-2">
                <Feature icon={<Search className="h-3.5 w-3.5" />} title="文献库">检索 arXiv、OpenAlex、Crossref 等来源，收藏、打标签、导出 BibTeX。</Feature>
                <Feature icon={<Languages className="h-3.5 w-3.5" />} title="PDF 划词翻译">在论文中选中任意文字，浮层里直接翻译或解释，不用离开阅读器。</Feature>
                <Feature icon={<GitBranch className="h-3.5 w-3.5" />} title="执行图">每个节点都有负责者、状态、日志和产物；需要确认的操作会暂停等你批准。</Feature>
              </div>
              <div className="mt-4 border-l-2 border-proof bg-proof-soft px-3 py-2.5 text-[11.5px] leading-5 text-ink-soft">
                <b className="text-ink">建议的第一件事：</b>上传一篇论文，然后在输入框问「分析这篇论文的主要创新点和局限性」。
              </div>
            </div>
          ) : null}

          {section === 'library' ? (
            <div>
              <p className="font-serif text-[13px] leading-6 text-ink-soft">
                文献库负责「找到、存住、带进会话」。搜索结果不会自动进入会话，需要你明确加入。
              </p>
              <ol className="mt-3 border-t border-line">
                <Step n="01" title="检索或导入">在「检索」里输入关键词，或把本地 PDF 拖进工作台。导入后会出现在「我的文献库」。</Step>
                <Step n="02" title="确认是否有 PDF">条目右侧会标「PDF」或「仅元数据」。只有元数据的条目需要先点「导入 PDF」。</Step>
                <Step n="03" title="加入当前会话">点「加入会话」，论文会出现在输入框上方的附件栏，跟着下一条消息一起提交。</Step>
                <Step n="04" title="在阅读器打开">点「打开 PDF」进入论文阅读器；阅读器里可以划词翻译，也可以整篇翻译。</Step>
              </ol>
              <div className="mt-4 border border-warn/25 bg-warn-soft px-3 py-2.5 text-[11.5px] leading-5 text-warn">
                <b>为什么「加入会话」是灰的？</b> 这条记录只有元数据，还没有可阅读的 PDF。先点「导入 PDF」，成功后再加入。
              </div>
              <div className="mt-3 border border-line bg-surface px-3 py-2.5 text-[11.5px] leading-5 text-muted">
                <b className="text-ink">BibTeX：</b>在文献条目上点「BibTeX」即可下载引用文件，用于论文写作。
              </div>
            </div>
          ) : null}

          {section === 'pdf' ? (
            <div>
              <p className="font-serif text-[13px] leading-6 text-ink-soft">
                阅读器有两种翻译方式：选中单句即时翻译，或整篇按页翻译。两者都使用真实模型，不会伪造译文。
              </p>
              <div className="mt-3 grid gap-2">
                <Feature icon={<Highlighter className="h-3.5 w-3.5" />} title="划词翻译 / 解释">
                  在 PDF 里按住鼠标选中任意一句，浮层会出现「翻译」和「解释」。结果还可以一键带回对话里追问。
                </Feature>
                <Feature icon={<Languages className="h-3.5 w-3.5" />} title="整篇翻译">
                  点工具栏「全文翻译」，按页排队翻译，可随时取消；完成后点页码查看每页译文。
                </Feature>
                <Feature icon={<FileSearch className="h-3.5 w-3.5" />} title="扫描件与 OCR">
                  扫描件会显示「OCR」徽标和置信度。低置信度页面会在顶部提醒，引用前请核对原文。
                </Feature>
              </div>
              <div className="mt-4 border-l-2 border-accent bg-accent-soft px-3 py-2.5 text-[11.5px] leading-5 text-ink-soft">
                <b className="text-ink">看不到翻译按钮？</b>先确认 PDF 有文本层。如果徽标显示「无文本」，划词和整篇翻译都不可用，需要先做 OCR 或换一份带文本层的 PDF。
              </div>
            </div>
          ) : null}

          {section === 'graph' ? (
            <div>
              <p className="font-serif text-[13px] leading-6 text-ink-soft">
                执行图把研究目标拆成可审计的节点。每个节点有负责者、依赖、状态和产物，执行过程不会被藏起来。
              </p>
              <ol className="mt-3 border-t border-line">
                <Step n="01" title="生成计划">在对话里发送研究目标后，系统会生成节点图。第一次会先确认意图和约束。</Step>
                <Step n="02" title="核对节点">点击节点查看负责者、任务类型和依赖；如果缺少材料，顶部会列出「缺失材料」和修复建议。</Step>
                <Step n="03" title="运行与审批">点「运行计划」启动。需要拉镜像、装依赖、下载数据或使用 GPU 时，会暂停并请求你批准。</Step>
                <Step n="04" title="查看结果">右侧节点详情里有日志、代码、图表和报告；完成后会显示验收状态。</Step>
              </ol>
              <div className="mt-4 grid gap-2">
                <Feature icon={<ListChecks className="h-3.5 w-3.5" />} title="状态怎么看">已完成、运行中、等待、失败、已取消。失败节点可以单独重试，不必重跑整张图。</Feature>
                <Feature icon={<Play className="h-3.5 w-3.5" />} title="自动运行">默认关闭。只有明确打开「创建计划后自动执行」且计划符合条件时，才会自动开始。</Feature>
              </div>
            </div>
          ) : null}
        </div>

        <footer className="shrink-0 border-t border-line bg-surface px-5 py-3">
          <p className="flex items-center gap-1.5 text-[11px] leading-5 text-muted">
            <ArrowRight className="h-3 w-3" />
            还有问题？直接在对话里问「这个工作台怎么用」，助手会结合当前界面回答。
          </p>
        </footer>
      </div>
    </div>
  );
}
