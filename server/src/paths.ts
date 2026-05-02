import fs from "node:fs/promises";
import path from "node:path";

/**
 * 校验手机端传入的工作区路径，第一版允许任意绝对目录但拒绝明显错误输入。
 */
export async function validateWorkspacePath(value: unknown): Promise<string> {
  if (typeof value !== "string") {
    throw new Error("工作区路径必须是字符串");
  }

  const trimmed = value.trim();
  if (trimmed.length === 0 || trimmed.length > 4096) {
    throw new Error("工作区路径长度不合法");
  }

  if (!path.isAbsolute(trimmed)) {
    throw new Error("工作区路径必须是绝对路径");
  }

  const resolved = path.resolve(trimmed);
  const stat = await fs.stat(resolved).catch(() => null);
  if (!stat) {
    throw new Error("工作区路径不存在");
  }
  if (!stat.isDirectory()) {
    throw new Error("工作区路径必须是目录");
  }

  return resolved;
}

