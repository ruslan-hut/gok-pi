import { useState } from "react";
import { login } from "./api";

interface LoginProps {
  onLogin: (token: string, expiresAt?: string) => void;
  onBack?: () => void;
}

export default function Login({ onLogin, onBack }: LoginProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(undefined);
    setLoading(true);

    try {
      const response = await login(username, password);
      if (response.token) {
        onLogin(response.token, response.expires_at);
      } else {
        // No auth configured, allow access
        onLogin("");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="login-container">
      <img src="/icon-192.png" alt="GOK-Pi" className="login-icon" />
      <div className="login-card">
        <h1>GOK-Pi Dashboard</h1>
        <p className="login-subtitle">Please sign in to continue</p>
        <form onSubmit={handleSubmit}>
          <div className="login-field">
            <label htmlFor="username">Username</label>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
              autoFocus
              disabled={loading}
            />
          </div>
          <div className="login-field">
            <label htmlFor="password">Password</label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              disabled={loading}
            />
          </div>
          {error && <div className="login-error">{error}</div>}
          <button type="submit" className="primary" disabled={loading}>
            {loading ? "Signing in..." : "Sign in"}
          </button>
        </form>
        {onBack && (
          <button type="button" className="login-back" onClick={onBack}>
            Continue without signing in
          </button>
        )}
      </div>
    </div>
  );
}

