import { useState } from 'react';
import { ArrowRight, CheckCircle2, KeyRound, Loader2, Shield, Sparkles } from 'lucide-react';
import type { DesktopBridge, DesktopCredentials } from '../../contracts/desktop';
import { getDesktopBridge } from '../../services/desktopBridge';

type Step = 'welcome' | 'credentials' | 'runtime' | 'done';

export interface OnboardingProps {
  onComplete: () => void;
  bridge?: DesktopBridge;
}

const providers = [
  { id: 'auto', label: '自动（推荐）', description: '检测到 Docker 时使用容器，否则在本机运行' },
  { id: 'native', label: '在本机运行', description: '无需安装 Docker，首次执行时按需下载 Python' },
  { id: 'docker', label: 'Docker 容器', description: '隔离更强，需要本机已安装 Docker' },
];

/** First-run setup: privacy, LLM credentials, runtime mode. */
export function Onboarding({ onComplete, bridge }: OnboardingProps) {
  const desktop = bridge ?? getDesktopBridge();
  const [step, setStep] = useState<Step>('welcome');
  const [baseUrl, setBaseUrl] = useState('https://api.deepseek.com/v1');
  const [model, setModel] = useState('deepseek-chat');
  const [apiKey, setApiKey] = useState('');
  const [provider, setProvider] = useState('auto');
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<'idle' | 'ok' | 'error'>('idle');
  const [testError, setTestError] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');

  const credentials: DesktopCredentials = { baseUrl: baseUrl.trim(), model: model.trim(), apiKey: apiKey.trim(), provider };
  const credentialsValid = /^https?:\/\//.test(credentials.baseUrl) && credentials.model.length > 0 && credentials.apiKey.length > 0;

  const testConnection = async () => {
    if (!credentialsValid) {
      setTestResult('error');
      setTestError('请填写 Base URL、模型和 API Key');
      return;
    }
    setTesting(true);
    setTestResult('idle');
    setTestError('');
    try {
      const result = await desktop?.testConnection(credentials);
      if (result && result.ok === false) {
        setTestResult('error');
        setTestError(result.error ?? '连接失败');
      } else {
        setTestResult('ok');
      }
    } catch (error) {
      setTestResult('error');
      setTestError(error instanceof Error ? error.message : '连接失败');
    } finally {
      setTesting(false);
    }
  };

  const finish = async () => {
    setSaving(true);
    setSaveError('');
    try {
      await desktop?.completeOnboarding(credentials);
      onComplete();
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : '保存设置失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-paper p-6 text-ink">
      <main className="w-full max-w-[680px] rounded-2xl border border-line bg-surface p-8 shadow-float">
        <div className="mb-6 flex items-center gap-2 text-[12px] text-ink-soft">
          {(['welcome', 'credentials', 'runtime', 'done'] as Step[]).map((entry, index) => (
            <span key={entry} className="flex items-center gap-2">
              <span className={step === entry ? 'flex h-5 w-5 items-center justify-center rounded-full bg-ink text-[11px] text-paper' : 'flex h-5 w-5 items-center justify-center rounded-full bg-surface-muted text-[11px] text-ink-soft'}>{index + 1}</span>
              {index < 3 ? <span className="h-px w-8 bg-line" /> : null}
            </span>
          ))}
        </div>

        {step === 'welcome' && (
          <section>
            <h1 className="flex items-center gap-2 text-[22px] font-semibold"><Sparkles className="h-5 w-5" /> 欢迎使用 ResearchAssistant</h1>
            <p className="mt-3 text-[13px] leading-6 text-ink-soft">代码与论文复现都在你自己的电脑上执行。API Key 通过 Windows DPAPI 加密保存，不会明文落盘，也不会上传代码。</p>
            <ul className="mt-4 space-y-2 text-[13px] text-ink-soft">
              <li className="flex items-center gap-2"><Shield className="h-4 w-4" /> 默认断网运行，联网需要审批</li>
              <li className="flex items-center gap-2"><KeyRound className="h-4 w-4" /> 密钥只注入子进程环境变量</li>
            </ul>
            <button type="button" onClick={() => setStep('credentials')} className="mt-6 inline-flex items-center gap-2 rounded-lg bg-ink px-4 py-2 text-[13px] font-medium text-paper">
              开始 <ArrowRight className="h-4 w-4" />
            </button>
          </section>
        )}

        {step === 'credentials' && (
          <section>
            <h1 className="text-[20px] font-semibold">配置模型连接</h1>
            <p className="mt-1 text-[12px] text-ink-soft">这些设置可以在之后随时修改。</p>
            <div className="mt-4 space-y-3">
              <label className="block text-[12px] text-ink-soft">
                Base URL
                <input aria-label="Base URL" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} className="mt-1 w-full rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink" />
              </label>
              <label className="block text-[12px] text-ink-soft">
                模型
                <input aria-label="模型" value={model} onChange={(event) => setModel(event.target.value)} className="mt-1 w-full rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink" />
              </label>
              <label className="block text-[12px] text-ink-soft">
                API Key
                <input aria-label="API Key" type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} className="mt-1 w-full rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink" />
              </label>
            </div>
            <div className="mt-4 flex items-center gap-3">
              <button type="button" onClick={() => void testConnection()} disabled={testing} className="inline-flex items-center gap-2 rounded-lg border border-line px-3 py-1.5 text-[12px] text-ink-soft">
                {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                测试连接
              </button>
              {testResult === 'ok' && <span className="inline-flex items-center gap-1 text-[12px] text-ok"><CheckCircle2 className="h-4 w-4" /> 连接成功</span>}
              {testResult === 'error' && <span className="text-[12px] text-proof">{testError}</span>}
            </div>
            <div className="mt-6 flex justify-between">
              <button type="button" onClick={() => setStep('welcome')} className="text-[12px] text-ink-soft">上一步</button>
              <button type="button" onClick={() => setStep('runtime')} disabled={!credentialsValid} className="inline-flex items-center gap-2 rounded-lg bg-ink px-4 py-2 text-[13px] font-medium text-paper disabled:opacity-40">
                下一步 <ArrowRight className="h-4 w-4" />
              </button>
            </div>
          </section>
        )}

        {step === 'runtime' && (
          <section>
            <h1 className="text-[20px] font-semibold">选择运行模式</h1>
            <p className="mt-1 text-[12px] text-ink-soft">桌面版默认使用本机运行时；检测到 Docker 时会自动升级到容器。</p>
            <div className="mt-4 space-y-2">
              {providers.map((entry) => (
                <button
                  key={entry.id}
                  type="button"
                  onClick={() => setProvider(entry.id)}
                  aria-pressed={provider === entry.id}
                  className={provider === entry.id ? 'w-full rounded-lg border border-ink bg-ink px-3 py-2 text-left text-paper' : 'w-full rounded-lg border border-line bg-paper px-3 py-2 text-left text-ink-soft hover:border-line-strong'}
                >
                  <span className="block text-[13px] font-medium">{entry.label}</span>
                  <span className="mt-0.5 block text-[11px] opacity-80">{entry.description}</span>
                </button>
              ))}
            </div>
            <p className="mt-4 text-[11px] text-ink-soft">首次执行会下载 Python 运行时（约 30MB），依赖（如 torch）按需安装。</p>
            <div className="mt-6 flex justify-between">
              <button type="button" onClick={() => setStep('credentials')} className="text-[12px] text-ink-soft">上一步</button>
              <button type="button" onClick={() => setStep('done')} className="inline-flex items-center gap-2 rounded-lg bg-ink px-4 py-2 text-[13px] font-medium text-paper">
                下一步 <ArrowRight className="h-4 w-4" />
              </button>
            </div>
          </section>
        )}

        {step === 'done' && (
          <section>
            <h1 className="flex items-center gap-2 text-[20px] font-semibold"><CheckCircle2 className="h-5 w-5 text-ok" /> 准备完成</h1>
            <p className="mt-2 text-[13px] text-ink-soft">模型：{model} · 运行模式：{providers.find((entry) => entry.id === provider)?.label}</p>
            {saveError && <p className="mt-2 text-[12px] text-proof">{saveError}</p>}
            <button type="button" onClick={() => void finish()} disabled={saving} className="mt-6 inline-flex items-center gap-2 rounded-lg bg-ink px-4 py-2 text-[13px] font-medium text-paper disabled:opacity-50">
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              进入工作台
            </button>
          </section>
        )}
      </main>
    </div>
  );
}

export default Onboarding;
