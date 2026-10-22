import { useCallback, useEffect, useRef, useState } from 'react';
import { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import { useAITranslationPlugin } from '../../hooks/useAITranslationPlugin';
import type { ChatMessage, UploadedFile } from '../../contracts/api';
import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import { isPdfAttachment, resolveAttachmentContentUrl } from '../../features/pdf-viewer/pdfAttachment';

interface UsePdfAssistFlowOptions {
  setPrompt: (value: string) => void;
  appendChatMessage: (message: ChatMessage) => void;
  appendSelectedTaskLog: (line: string) => void;
}

export function usePdfAssistFlow(options: UsePdfAssistFlowOptions) {
  const { setPrompt, appendChatMessage, appendSelectedTaskLog } = options;
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);
  const pdfUrlRef = useRef<string | null>(null);
  const [isFullTranslating, setIsFullTranslating] = useState(false);
  const [showSuggestions, setShowSuggestions] = useState(true);
  const defaultLayoutPluginInstance = defaultLayoutPlugin();

  const handleAskAI = useCallback(
    (selectedText: string) => {
      setPrompt(`请帮我详细解释这篇文献中的这段内容：\n"${selectedText}"`);
      appendSelectedTaskLog('[System] 已获取划词内容，准备向 Research Assistant Agent 发起追问...');
    },
    [appendSelectedTaskLog, setPrompt],
  );

  const aiTranslationPluginInstance = useAITranslationPlugin(handleAskAI);

  const replacePdfUrl = useCallback((next: string | null) => {
    const previous = pdfUrlRef.current;
    if (previous?.startsWith('blob:') && previous !== next) {
      URL.revokeObjectURL(previous);
    }
    pdfUrlRef.current = next;
    setPdfUrl(next);
  }, []);

  useEffect(() => () => {
    if (pdfUrlRef.current?.startsWith('blob:')) {
      URL.revokeObjectURL(pdfUrlRef.current);
    }
  }, []);

  const openPdfFromUpload = useCallback(
    async (upload: UploadedFile) => {
      if (!isPdfAttachment(upload)) {
        appendChatMessage({ role: 'system', text: '该附件不是 PDF，无法在阅读器中打开。' });
        return;
      }
      const url = resolveAttachmentContentUrl(API_BASE_URL, upload.content_url);
      if (!url) {
        appendChatMessage({ role: 'system', text: '该 PDF 没有可用的内容地址，请重新上传。' });
        return;
      }
      try {
        const response = await fetch(url, {
          credentials: 'include',
          headers: API_AUTH_TOKEN ? { Authorization: `Bearer ${API_AUTH_TOKEN}` } : undefined,
        });
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        const blob = await response.blob();
        if (!blob.size) {
          throw new Error('empty PDF');
        }
        replacePdfUrl(URL.createObjectURL(blob));
      } catch (error) {
        console.error(error);
        appendChatMessage({ role: 'system', text: 'PDF 读取失败；请确认文件已上传且当前会话有访问权限。' });
      }
    },
    [appendChatMessage, replacePdfUrl],
  );

  const closePdf = useCallback(() => {
    replacePdfUrl(null);
  }, [replacePdfUrl]);

  const handleFullTranslation = useCallback(async () => {
    setIsFullTranslating(true);
    appendChatMessage({
      role: 'system',
      text: '全文翻译服务尚未接入后端；当前只能使用划词追问。请勿将本功能视为可用的翻译结果。',
    });
    setIsFullTranslating(false);
  }, [appendChatMessage]);

  return {
    pdfUrl,
    setPdfUrl: replacePdfUrl,
    openPdfFromUpload,
    closePdf,
    isFullTranslating,
    showSuggestions,
    setShowSuggestions,
    defaultLayoutPluginInstance,
    aiTranslationPluginInstance,
    handleFullTranslation,
  };
}
