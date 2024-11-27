import { build } from 'esbuild';
import fs from 'fs';
import fsextra from 'fs-extra';
import { exec, execSync } from 'child_process';
import path from 'path';

console.log("Starting node build");

const __dirname = new URL('.', import.meta.url).pathname;

function mkdir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

const srcDir = path.join(__dirname, 'src');
const distDir = path.join(__dirname, 'dist');
mkdir(distDir);
mkdir(path.join(distDir, 'static'));
mkdir(path.join(distDir, 'templates'));
mkdir(path.join(srcDir, 'generated'));

const version = Date.now(); // or use any unique versioning scheme

const protocCommand = `
  protoc --ts_out src/generated --proto_path ../proto ../proto/*.proto
`;

function runCommand(cmd) {
  try {
    // Execute the command synchronously
    const stdout = execSync(cmd, { stdio: 'pipe' }); // 'pipe' captures output
    console.log(`stdout: ${stdout.toString()}`); // Convert Buffer to string
  } catch (error) {
    // Handle errors
    console.error(`exec error: ${error.message}`);
    if (error.stdout) {
      console.error(`stdout: ${error.stdout.toString()}`);
    }
    if (error.stderr) {
      console.error(`stderr: ${error.stderr.toString()}`);
    }
    process.exit(1); // Exit with an error code
  }
}

console.log("Generating protobufs");
runCommand(protocCommand);

console.log("Checking typescript types");
runCommand(`tsc --noEmit`);

function updateHtml(input, output) {
  const indexInPath = path.join(__dirname, input);
  const indexOutPath = path.join(__dirname, output);
  const indexContent = fs.readFileSync(indexInPath, 'utf-8');
  
  const updatedContent = indexContent
    .replace(/bundle\.js/g, `bundle.js?v=${version}`)
    .replace(/styles\.css/g, `styles.css?v=${version}`);
  
  fs.writeFileSync(indexOutPath, updatedContent);
}

console.log("Preparing html static and templates");
updateHtml('html/index.html', 'dist/static/index.html');
updateHtml('html/generic.html', 'dist/templates/generic.html');

console.log("Copying static resources");
fsextra.copy('public', 'dist/static', { overwrite: true });

console.log("esbuild of frontend");
build({
  entryPoints: ['./src/index.tsx'],
  loader: {'.ts': 'ts', '.tsx': 'tsx', '.js': 'js'},
  tsconfig: 'tsconfig.json',
  jsx: 'automatic',
  bundle: true,
  outfile: 'dist/static/bundle.js',
  minify: true, 
  sourcemap: true,
  target: ['es2016'],
}).catch(() => process.exit(1));

console.log("Finished node build");