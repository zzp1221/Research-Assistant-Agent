import { Component, type ErrorInfo, type ReactNode } from 'react';

interface AppErrorBoundaryProps {
  children: ReactNode;
}

interface AppErrorBoundaryState {
  hasError: boolean;
}

export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  public state: AppErrorBoundaryState = {
    hasError: false,
  };

  public static getDerivedStateFromError(): AppErrorBoundaryState {
    return { hasError: true };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    console.error('[AppErrorBoundary] Unhandled render error:', error, errorInfo);
  }

  public render(): ReactNode {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-[100dvh] items-center justify-center bg-paper p-6">
          <div className="w-full max-w-lg rounded-[8px] border border-line bg-surface p-6 shadow-[var(--shadow-card)]">
            <h1 className="font-serif text-lg font-semibold text-flag">页面出现异常</h1>
            <p className="mt-2 text-[12px] leading-6 text-muted">
              应用遇到了未处理错误。请刷新页面重试，或检查控制台日志定位问题。
            </p>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
