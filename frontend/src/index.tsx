import React, { useEffect } from 'react';
import ReactDOM from 'react-dom';
import { useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { SfuApp, StatusApp, TrunkApp } from './umbrella/SfuApp'

const EmptyApp = () => {
    return (
      <div>
          <p>This is the default empty page, shouldn't be seen.</p>
      </div>
    );
  };

const injected = window.__injected__;

// Prefix when deployed
const httpPrefix = injected.HttpPrefix;

function removePrefix(path: string) : string {
    if(httpPrefix.length > 0 && path.startsWith(httpPrefix)) {
        path = path.substring(httpPrefix.length, path.length);
    }

    return path;
}

export function runApp() {
  const root = createRoot(document.getElementById('main-container')!);

  console.log("Creating app on page "+window.location.pathname);

  switch(removePrefix(window.location.pathname)) {
  case "/sfu":
    root.render(<SfuApp />);
    break;
  case "/trunk":
    root.render(<TrunkApp />);
    break;
  case "/status":
    root.render(<StatusApp />);
    break;
  default:
    root.render(<EmptyApp />);
    break;
  }
};

runApp();