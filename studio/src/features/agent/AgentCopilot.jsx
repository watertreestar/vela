import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Card,
  Drawer,
  Empty,
  Input,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  Bot,
  Check,
  FileText,
  Send,
  ShieldAlert,
  Square,
  Wrench,
  X,
} from 'lucide-react';
import './agent-ui.css';

const { Text, Paragraph } = Typography;

const TOOL_STATE = {
  planned: { color: 'default', label: '已计划' },
  'approval-pending': { color: 'gold', label: '等待确认' },
  executing: { color: 'processing', label: '执行中' },
  succeeded: { color: 'success', label: '已完成' },
  failed: { color: 'error', label: '失败' },
};

function formatValue(value) {
  if (value === undefined || value === null || value === '') return '未提供';
  if (typeof value === 'string') return value;

  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function summarizeInput(input) {
  const text = formatValue(input);
  return text.length > 520 ? `${text.slice(0, 520)}…` : text;
}

function getContextLabel(context) {
  if (!context) return '当前构建上下文';
  if (typeof context === 'string') return context;
  return context.label || context.name || context.projectName || '当前构建上下文';
}

/**
 * Informational approval UI only. Authorization and approval binding must be
 * validated again on the server before any production operation is performed.
 */
export function AgentApprovalCard({ tool, onApprove, onDeny }) {
  const [submitted, setSubmitted] = useState(false);
  const approvalKey = tool.approvalId || tool.toolCallId;
  const risk = tool.risk || '请确认此操作的影响范围后再继续。';

  const handleDecision = (decision) => {
    if (!approvalKey || submitted) return;
    setSubmitted(true);
    const callback = decision === 'approve' ? onApprove : onDeny;
    callback?.({
      approvalId: tool.approvalId,
      toolCallId: tool.toolCallId,
      toolName: tool.name,
      tool,
    });
  };

  return (
    <Card size="small" className="agent-copilot__approval-card" aria-label={`工具审批：${tool.name || '未命名工具'}`}>
      <Space align="start" size={10}>
        <span className="agent-copilot__risk-icon" aria-hidden="true"><ShieldAlert size={18} /></span>
        <div>
          <Text strong>需要确认：{tool.name || '未命名工具'}</Text>
          <Paragraph className="agent-copilot__approval-copy">
            影响范围 / 参数摘要
          </Paragraph>
          <pre className="agent-copilot__payload">{summarizeInput(tool.input)}</pre>
          <Text type="warning">风险：{risk}</Text>
        </div>
      </Space>
      <Alert
        className="agent-copilot__server-note"
        type="info"
        showIcon
        message="服务器会再次校验权限与审批绑定"
      />
      <Space className="agent-copilot__approval-actions" wrap>
        <Button
          type="primary"
          icon={<Check size={15} />}
          disabled={!approvalKey || submitted}
          onClick={() => handleDecision('approve')}
        >
          确认执行
        </Button>
        <Button
          danger
          icon={<X size={15} />}
          disabled={!approvalKey || submitted}
          onClick={() => handleDecision('deny')}
        >
          拒绝
        </Button>
      </Space>
    </Card>
  );
}

export function AgentToolCard({ tool, onApprove, onDeny }) {
  const state = TOOL_STATE[tool.state] || TOOL_STATE.planned;
  const hasDetails = tool.input !== undefined || tool.output !== undefined || tool.error;

  return (
    <div className="agent-copilot__tool-block" aria-label={`工具调用：${tool.name || '未命名工具'}`}>
      <Card size="small" className="agent-copilot__tool-card">
        <Space className="agent-copilot__tool-heading" align="start">
          <span className="agent-copilot__tool-icon" aria-hidden="true"><Wrench size={16} /></span>
          <div>
            <Text strong>{tool.name || '未命名工具'}</Text>
            <div><Tag color={state.color}>{state.label}</Tag></div>
          </div>
          {tool.state === 'executing' && <Spin size="small" aria-label="工具正在执行" />}
        </Space>
        {hasDetails && (
          <div className="agent-copilot__tool-details">
            {tool.input !== undefined && <><Text type="secondary">参数</Text><pre className="agent-copilot__payload">{summarizeInput(tool.input)}</pre></>}
            {tool.output !== undefined && <><Text type="secondary">结果</Text><pre className="agent-copilot__payload">{summarizeInput(tool.output)}</pre></>}
            {tool.error && <Alert type="error" showIcon message={String(tool.error)} />}
          </div>
        )}
      </Card>
      {tool.state === 'approval-pending' && (
        <AgentApprovalCard tool={tool} onApprove={onApprove} onDeny={onDeny} />
      )}
    </div>
  );
}

function MessagePart({ part, onApprove, onDeny }) {
  switch (part.type) {
    case 'text':
      return <Paragraph className="agent-copilot__message-text">{part.text}</Paragraph>;
    case 'reasoning-summary':
      return (
        <div className="agent-copilot__stage-summary" aria-label="Agent 阶段摘要">
          <Text type="secondary">阶段摘要：{part.text}</Text>
        </div>
      );
    case 'tool':
      return <AgentToolCard tool={part} onApprove={onApprove} onDeny={onDeny} />;
    case 'source':
      return part.href ? (
        <a className="agent-copilot__source" href={part.href} target="_blank" rel="noreferrer">
          <FileText size={14} aria-hidden="true" /> {part.label || '参考来源'}
        </a>
      ) : (
        <span className="agent-copilot__source"><FileText size={14} aria-hidden="true" /> {part.label || '参考来源'}</span>
      );
    default:
      return null;
  }
}

export function AgentMessageList({ messages = [], status, onApprove, onDeny }) {
  const bottomRef = useRef(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }, [messages, status]);

  if (!messages.length) {
    return (
      <div className="agent-copilot__empty-state" aria-label="Agent 对话空状态">
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有对话">
          <Text type="secondary">服务端 Agent / BFF 尚未配置时，不会执行任何生产操作。</Text>
        </Empty>
      </div>
    );
  }

  return (
    <div className="agent-copilot__messages" aria-label="Agent 对话消息" aria-live="polite">
      {messages.map((message) => (
        <article key={message.id} className={`agent-copilot__message agent-copilot__message--${message.role || 'assistant'}`}>
          <div className="agent-copilot__message-label">
            {message.role === 'assistant' && <Bot size={15} aria-hidden="true" />}
            {message.role === 'user' ? '你' : message.role === 'system' ? '系统' : '构建助手'}
          </div>
          <div className="agent-copilot__message-parts">
            {(message.parts || []).map((part, index) => (
              <MessagePart
                key={`${message.id}-${part.toolCallId || part.approvalId || part.type}-${index}`}
                part={part}
                onApprove={onApprove}
                onDeny={onDeny}
              />
            ))}
          </div>
        </article>
      ))}
      {status === 'streaming' && (
        <div className="agent-copilot__typing" aria-label="Agent 正在生成回复"><Spin size="small" /> 正在整理回复…</div>
      )}
      <div ref={bottomRef} />
    </div>
  );
}

export function AgentCopilotPanel({
  open,
  onClose,
  context,
  messages = [],
  status,
  error,
  notice,
  onSend,
  onStop,
  onApprove,
  onDeny,
}) {
  const [draft, setDraft] = useState('');
  const isBusy = ['submitted', 'streaming'].includes(status);
  const canSend = Boolean(draft.trim()) && !isBusy;
  const contextLabel = useMemo(() => getContextLabel(context), [context]);

  const send = () => {
    const content = draft.trim();
    if (!content || isBusy) return;
    onSend?.(content);
    setDraft('');
  };

  const handleKeyDown = (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send();
    }
  };

  return (
    <Drawer
      title={<span className="agent-copilot__title"><Bot size={18} aria-hidden="true" /> 构建助手</span>}
      placement="right"
      open={open}
      onClose={onClose}
      width={480}
      className="agent-copilot"
      rootClassName="agent-copilot-root"
      aria-label="构建助手面板"
      footer={
        <div className="agent-copilot__composer" aria-label="向构建助手发送消息">
          <Input.TextArea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={handleKeyDown}
            autoSize={{ minRows: 2, maxRows: 5 }}
            placeholder="描述要检查、生成或执行的构建任务"
            aria-label="Agent 消息输入"
            disabled={isBusy}
          />
          <div className="agent-copilot__composer-actions">
            <Text type="secondary">Enter 发送 · Shift + Enter 换行</Text>
            {isBusy ? (
              <Tooltip title="停止生成"><Button icon={<Square size={15} />} onClick={onStop} aria-label="停止 Agent" /></Tooltip>
            ) : (
              <Button type="primary" icon={<Send size={16} />} disabled={!canSend} onClick={send}>发送</Button>
            )}
          </div>
        </div>
      }
    >
      <div className="agent-copilot__context" aria-label="当前 Agent 上下文">
        <Badge status="processing" text={contextLabel} />
      </div>
      <Alert
        className="agent-copilot__mode-note"
        type="info"
        showIcon
        message="回复、工具调用与审批是三种不同状态"
        description="文字回复只提供建议；工具调用会展示名称、参数和结果；有生产影响的操作必须先进入等待确认状态，并由服务端再次校验授权与审批绑定。"
      />
      {notice && <Alert className="agent-copilot__notice" type="warning" showIcon message="当前为说明模式" description={String(notice)} />}
      {error && <Alert className="agent-copilot__error" type="error" showIcon message="Agent 请求失败" description={String(error)} />}
      <AgentMessageList messages={messages} status={status} onApprove={onApprove} onDeny={onDeny} />
    </Drawer>
  );
}
